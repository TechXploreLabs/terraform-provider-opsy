Step 1: Create the archive.zip file

```bash
pushd opsyyamls; zip -r archive.zip . ; mv archive.zip ../; popd"
```

Step 2: terraform init

Step 3: terraform plan

Step 4: terraform apply -auto-approve

Step 5: terraform destroy -auto-approve