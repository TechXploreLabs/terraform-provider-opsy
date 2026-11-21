package provider

// Async stack support (local and http modes). Replaces opsy_v2/operation.go.
//
// Reserved vars, owned by the provider. The whole `seristack_` prefix is
// reserved and rejected in the resource's vars/sensitive at plan time:
//
//	seristack_status  "null" on the first call, "pending" on every status check
//	seristack_status_vars  absent on the first call; on status checks the compact JSON
//	             string the stack returned in its pending reply
//	id           sent on status checks when known (always for Update/Delete)
//
// Stack replies (top-level JSON object):
//
//	{"seristack_status":"pending",   "id":"<optional>", "seristack_status_vars":{...}, "seristack_retry":"10m"}
//	{"seristack_status":"succeeded", "id":"<id>", "output":{...}}
//	{"seristack_status":"failed",    "error":"why"}
//	no "seristack_status" key -> synchronous, exactly as before
//
// Flow: first call (seristack_status=null) -> if pending, wait `retry`, call the SAME
// stack and method again with seristack_status=pending + seristack_status_vars (+ id) -> repeat
// until succeeded / failed / the Terraform timeout ends.
//
// `id` should be returned as soon as the cloud has assigned one: Create then
// saves it to state immediately, so a failed or interrupted wait leaves a
// tainted resource instead of an orphan. It must be the same id in the
// pending and the succeeded reply.
//
// The status branch of the stack must be read-only and must validate
// seristack_status_vars (allowed_regex on the var, jq to parse it): anyone who can call
// the endpoint can send seristack_status=pending with any seristack_status_vars. Never put
// secrets in seristack_status_vars.

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	opsyseristackaction "github.com/TechXploreLabs/seristack/pkg/opsy"
)

type stackResult = opsyseristackaction.Result

const (
	varStatus     = "seristack_status"
	keyRetry      = "seristack_retry"
	varStatusVars = "seristack_status_vars"

	statusNullValue = "null" // sent on the first call
	statusPending   = "pending"
	statusSucceeded = "succeeded"
	statusFailed    = "failed"

	defaultRetry       = 10 * time.Second
	minRetry           = 2 * time.Second
	maxRetry           = time.Hour
	maxPollNetErrors   = 5
	maxStatusVarsBytes = 2048
)

// OperationError carries the handle so diagnostics can tell the user that
// something may still be running outside Terraform.
type OperationError struct {
	ID         string
	StatusVars string
	Err        error
}

func (e *OperationError) Error() string {
	var h []string
	if e.ID != "" {
		h = append(h, fmt.Sprintf("id=%q", e.ID))
	}
	if e.StatusVars != "" {
		h = append(h, "seristack_status_vars="+truncate(e.StatusVars, 300))
	}
	if len(h) == 0 {
		return e.Err.Error()
	}
	return fmt.Sprintf("%s (%s; the operation may still be running on the remote side)",
		e.Err, strings.Join(h, " "))
}

func (e *OperationError) Unwrap() error { return e.Err }

// startedFunc is called once, as soon as an id is known while pending.
type startedFunc func(ctx context.Context, id string) error

type stackReply struct {
	status        string
	id            string
	retry         time.Duration
	message       string
	statusVars    string // compact JSON text, "" if none
	statusVarsErr error
	raw           map[string]json.RawMessage
}

// parseReply returns ok=false when the output is not a JSON object with a
// top-level "seristack_status" (= a synchronous stack).
func parseReply(output string) (stackReply, bool) {
	var raw map[string]json.RawMessage
	if err := json.Unmarshal([]byte(strings.TrimSpace(output)), &raw); err != nil {
		return stackReply{}, false
	}
	st, present := raw[varStatus]
	if !present {
		return stackReply{}, false
	}
	status := strings.ToLower(strings.TrimSpace(jsonString(st)))
	if status == "" {
		return stackReply{}, false
	}
	sv, svErr := compactStatusVars(raw[varStatusVars])
	return stackReply{
		status:        status,
		id:            jsonString(raw["id"]),
		retry:         parseRetry(raw[keyRetry]),
		message:       firstNonEmpty(jsonString(raw["error"]), jsonString(raw["message"])),
		statusVars:    sv,
		statusVarsErr: svErr,
		raw:           raw,
	}, true
}

// compactStatusVars accepts seristack_status_vars as a JSON object/array or as a string
// (JSON text), and returns compact JSON text for the query string.
func compactStatusVars(raw json.RawMessage) (string, error) {
	t := bytes.TrimSpace(raw)
	if len(t) == 0 || string(t) == "null" {
		return "", nil
	}
	text := string(t)
	var s string
	if json.Unmarshal(t, &s) == nil {
		text = strings.TrimSpace(s)
		if text == "" {
			return "", nil
		}
	}
	var buf bytes.Buffer
	if json.Valid([]byte(text)) {
		if err := json.Compact(&buf, []byte(text)); err == nil {
			text = buf.String()
		}
	}
	if len(text) > maxStatusVarsBytes {
		return "", fmt.Errorf("seristack_status_vars is %d bytes; the limit is %d. Keep it minimal "+
			"(just the handle needed to recheck)", len(text), maxStatusVarsBytes)
	}
	return text, nil
}

// runOperation runs the stack once (seristack_status=null) and, if it answers
// "pending", keeps calling it (seristack_status=pending + seristack_status_vars + id) until it
// succeeds, fails, or ctx ends.
func runOperation(ctx context.Context, runner StackRunner, typeName, stack string,
	vars map[string]string, onStarted startedFunc) (stackResult, error) {
	return runOperationProgress(ctx, runner, typeName, stack, vars, onStarted, nil)
}

// runOperationProgress is runOperation plus an optional onWait hook, called
// before every wait with the time the provider is about to sleep. The action
// uses it to send progress messages to Terraform.
func runOperationProgress(ctx context.Context, runner StackRunner, typeName, stack string,
	vars map[string]string, onStarted startedFunc, onWait func(wait time.Duration)) (stackResult, error) {

	first := cloneVars(vars)
	first[varStatus] = statusNullValue
	delete(first, varStatusVars)

	res, err := runner.Run(ctx, typeName, stack, first)
	if err != nil {
		return res, err
	}
	if !res.Success {
		return res, nil
	}

	reply, async := parseReply(res.Output)
	if !async {
		return res, nil
	}
	switch reply.status {
	case statusSucceeded:
		return res, nil
	case statusFailed:
		return failedResult(res, reply), nil
	case statusPending:
		// continue below
	default:
		return res, unknownStatusError(res.Name, reply.status)
	}
	if reply.statusVarsErr != nil {
		return res, fmt.Errorf("stack %q: %w", res.Name, reply.statusVarsErr)
	}

	id := firstNonEmpty(reply.id, vars["id"])
	sv := reply.statusVars
	if id == "" && sv == "" {
		return res, fmt.Errorf("stack %q returned status \"pending\" with neither id nor seristack_status_vars, "+
			"so there is nothing to check later", res.Name)
	}

	wrap := func(err error) error { return &OperationError{ID: id, StatusVars: sv, Err: err} }

	notified := false
	notify := func() error {
		if notified || id == "" || onStarted == nil {
			return nil
		}
		notified = true
		return onStarted(ctx, id)
	}
	if err := notify(); err != nil {
		return res, wrap(err)
	}

	pollVars := cloneVars(vars)
	pollVars[varStatus] = statusPending
	setOpt := func(k, v string) {
		if v != "" {
			pollVars[k] = v
		} else {
			delete(pollVars, k)
		}
	}
	setOpt("id", id)
	setOpt(varStatusVars, sv)

	wait := reply.retry
	netErrs := 0

	for {
		if onWait != nil {
			onWait(wait)
		}
		if err := sleepCtx(ctx, wait); err != nil {
			return res, wrap(waitStopped(typeName, stack, err))
		}

		r, err := runner.Run(ctx, typeName, stack, pollVars)
		if err != nil {
			if ctx.Err() != nil {
				return res, wrap(waitStopped(typeName, stack, ctx.Err()))
			}
			netErrs++
			if netErrs >= maxPollNetErrors {
				return r, wrap(fmt.Errorf("%d consecutive errors while checking status: %w", netErrs, err))
			}
			continue // transient: try again after the same wait
		}
		netErrs = 0

		if !r.Success {
			return r, nil // reported as usual by the caller
		}

		pr, async := parseReply(r.Output)
		if !async {
			return r, wrap(fmt.Errorf("stack %q answered a status check without a \"seristack_status\" field; "+
				"the branch that runs when seristack_status=pending must print one", r.Name))
		}

		switch pr.status {
		case statusPending:
			if pr.statusVarsErr != nil {
				return r, wrap(fmt.Errorf("stack %q: %w", r.Name, pr.statusVarsErr))
			}
			if pr.statusVars != "" { // the recheck data may evolve between calls
				sv = pr.statusVars
				setOpt(varStatusVars, sv)
			}
			if id == "" && pr.id != "" { // id became known later
				id = pr.id
				setOpt("id", id)
				if err := notify(); err != nil {
					return r, wrap(err)
				}
			}
			wait = pr.retry
		case statusSucceeded:
			return withID(r, pr, id), nil
		case statusFailed:
			return failedResult(r, pr), nil
		default:
			return r, wrap(unknownStatusError(r.Name, pr.status))
		}
	}
}

func failedResult(res stackResult, reply stackReply) stackResult {
	return stackResult{
		Name:    res.Name,
		Success: false,
		Error:   firstNonEmpty(reply.message, "the stack reported status \"failed\""),
		Output:  res.Output,
	}
}

func withID(res stackResult, reply stackReply, id string) stackResult {
	if id == "" || jsonString(reply.raw["id"]) != "" {
		return res
	}
	idJSON, err := json.Marshal(id)
	if err != nil {
		return res
	}
	reply.raw["id"] = idJSON
	out, err := json.Marshal(reply.raw)
	if err != nil {
		return res
	}
	res.Output = string(out)
	return res
}

func unknownStatusError(name, status string) error {
	return fmt.Errorf("stack %q returned unknown status %q (expected pending, succeeded or failed)", name, status)
}

func waitStopped(typeName, stack string, cause error) error {
	if errors.Is(cause, context.DeadlineExceeded) {
		return fmt.Errorf("timed out waiting for %s_%s; raise the `timeouts` setting if the cloud needs longer", typeName, stack)
	}
	return fmt.Errorf("stopped waiting for %s_%s: %w", typeName, stack, cause)
}

func parseRetry(raw json.RawMessage) time.Duration {
	d := defaultRetry
	if s := strings.TrimSpace(jsonString(raw)); s != "" {
		if p, err := time.ParseDuration(s); err == nil {
			d = p
		} else if n, err := strconv.ParseFloat(s, 64); err == nil {
			d = time.Duration(n * float64(time.Second))
		}
	}
	if d < minRetry {
		return minRetry
	}
	if d > maxRetry {
		return maxRetry
	}
	return d
}

func jsonString(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	var s string
	if json.Unmarshal(raw, &s) == nil {
		return s
	}
	t := strings.TrimSpace(string(raw))
	if t == "null" || strings.HasPrefix(t, "{") || strings.HasPrefix(t, "[") {
		return ""
	}
	return t
}

func sleepCtx(ctx context.Context, d time.Duration) error {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}

func firstNonEmpty(ss ...string) string {
	for _, s := range ss {
		if s != "" {
			return s
		}
	}
	return ""
}

func cloneVars(in map[string]string) map[string]string {
	out := make(map[string]string, len(in)+3)
	for k, v := range in {
		out[k] = v
	}
	return out
}
