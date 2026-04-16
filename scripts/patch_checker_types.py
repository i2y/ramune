#!/usr/bin/env python3
"""Patch checker/types.go to expose Type.alias via a public Alias() accessor.

Upstream typescript-go keeps the alias field unexported. The gotranspiler
needs read access to resolve type aliases during TS->Go mapping, so we
add a thin accessor here and rerun it on every sync.
"""
import sys

filepath = sys.argv[1]

with open(filepath) as f:
    content = f.read()

accessor = """func (t *Type) Alias() *TypeAlias {
\treturn t.alias
}

"""

if "func (t *Type) Alias() *TypeAlias" in content:
    print(f"Skipped {filepath} (already patched)")
    sys.exit(0)

anchor = "func (t *Type) Flags() TypeFlags {"
if anchor not in content:
    print(f"ERROR: anchor not found in {filepath}", file=sys.stderr)
    sys.exit(1)

content = content.replace(anchor, accessor + anchor, 1)

with open(filepath, "w") as f:
    f.write(content)

print(f"Patched {filepath}")
