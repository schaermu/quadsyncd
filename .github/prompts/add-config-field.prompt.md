---
mode: "agent"
description: "Add a new configuration field to quadsyncd"
---

Add a new configuration field named `${fieldName}` to quadsyncd.

Follow these steps in order:

1. **`internal/config/config.go`** – Add the new field to the appropriate struct.
   - Use the correct Go type (string, bool, int, etc.).
   - Tag with `yaml:"field_name"`.
   - If the field is a file path, it must be validated as an absolute path after
     environment variable expansion.

2. **`internal/config/config.go` – `Validate()` method** – Add validation logic:
   - Required fields: return an error if empty.
   - Path fields: expand `${HOME}` / `${VAR}` with `os.ExpandEnv`, then check
     `filepath.IsAbs(...)`.
   - Enum fields: check against the allowed set and return a descriptive error.

3. **`config.example.yaml`** – Add a commented-out example entry for the new field,
   placed under the correct section with a one-line comment explaining its purpose.

4. **`internal/config/config_test.go`** – Add test cases covering:
   - Valid value accepted.
   - Invalid/missing value rejected with an appropriate error message.
   - (Path fields only) Relative path rejected; absolute path accepted.

After making the changes, run:
```bash
go mod tidy && make fmt && make lint && make test
```
Fix any issues before finishing.
