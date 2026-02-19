---
mode: "agent"
description: "Add support for a new Quadlet file extension"
---

Add support for the new Quadlet file extension `${extension}` (e.g., `.network`,
`.volume`, `.image`).

Follow these steps in order:

1. **`internal/quadlet/quadlet.go`** – Append `"${extension}"` to the
   `ValidExtensions` slice.

2. **Verify `UnitNameFromQuadlet`** – Confirm that the existing logic still produces
   a correct `.service` unit name for the new extension. No change should be needed,
   but double-check the transformation.

3. **`internal/quadlet/quadlet_test.go`** – Add a table-driven test case for the new
   extension:
   - A file `example${extension}` should be detected as a valid quadlet.
   - `UnitNameFromQuadlet("example${extension}")` should return `"example.service"`.

After making the changes, run:
```bash
make fmt && make lint && make test
```
Fix any issues before finishing.
