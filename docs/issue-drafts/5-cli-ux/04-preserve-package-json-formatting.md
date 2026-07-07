TITLE: Preserve package.json key order and formatting when editing dependencies
LABELS: bug
---
## Severity

Medium — every `add`/`remove` produces a large spurious diff in the user's `package.json`, polluting code review and git blame.

## Background

lnpm edits the consumer project's `package.json` to add or remove a `file:.lnpm/<pkg>` dependency reference. `add` and `remove` both need to touch exactly one key inside `dependencies` or `devDependencies` and leave everything else untouched.

## Problem

`updatePackageJSON` (used by `add`), and `restorePackageJSON`/`removeFromPackageJSON` (used by `remove`/`retreat`), all unmarshal the whole file into a `map[string]interface{}` and re-serialize it with `json.MarshalIndent`. Go maps have no defined iteration order, and `encoding/json` sorts map keys alphabetically when marshaling. The result:

- Every top-level key in `package.json` (`name`, `version`, `scripts`, `dependencies`, ...) and every key inside each dependency object gets reordered alphabetically.
- The user's original indentation style (tabs, 4-space, trailing commas via a formatter) is replaced with a fixed 2-space indent.
- Any numeric value larger than 2^53 in the file round-trips through `float64` and can be corrupted.

Running `lnpm add my-lib` on a normal `package.json` produces a diff touching nearly every line, even though only one dependency line actually changed.

## Where to look

- `internal/cli/add.go:415-489` — `updatePackageJSON`: `json.Unmarshal(data, &pkgJSON)` into `map[string]interface{}` at line 423, `json.MarshalIndent(pkgJSON, "", "  ")` at line 476.
- `internal/cli/remove.go:136-166` — `restorePackageJSON`: same map-based unmarshal/marshal round trip.
- `internal/cli/remove.go:169-196` — `removeFromPackageJSON`: same pattern.

## How to fix

1. Replace the full-map round trip with an order-preserving edit. A practical approach: unmarshal only as far as needed to locate the target key using `json.RawMessage` (e.g. `map[string]json.RawMessage` for the top level, then decode just the `dependencies`/`devDependencies` value as `map[string]json.RawMessage` too), mutate only that one key, and re-marshal only that submap — leaving all sibling keys as their original untouched `json.RawMessage` bytes.
2. Alternatively, do a targeted text edit: locate the `"dependencies"` (or `"devDependencies"`) object in the raw source and insert/replace/remove the single `"<pkg>": "<value>"` line via string/regex manipulation, matching the surrounding indentation detected from the file. This mirrors what `npm` itself does when editing `package.json`.
3. Whichever approach is chosen, apply it consistently to `updatePackageJSON`, `restorePackageJSON`, and `removeFromPackageJSON` — all three have the same bug.
4. Preserve the trailing newline handling already present (`output = append(output, '\n')`).

## Acceptance criteria

- [ ] `lnpm add my-lib` on a `package.json` with non-alphabetical key order changes only the `dependencies`/`devDependencies` block, leaving all other keys and their order untouched.
- [ ] `lnpm remove my-lib` and `lnpm retreat` likewise touch only the affected dependency line.
- [ ] Existing indentation style (2-space, 4-space, tabs) of the untouched parts of the file is preserved.
- [ ] A `package.json` containing an integer literal beyond `2^53` is not altered by an unrelated add/remove.

## Testing

Add unit tests in `internal/cli/add_test.go` and a new or existing `internal/cli/remove_test.go` (there is currently no `internal/cli/remove_test.go`; add-adjacent coverage lives in `tests/add_test.go` and `tests/remove_test.go`) that:
- write a `package.json` fixture with keys in non-alphabetical order and 4-space indentation,
- call `updatePackageJSON`/`removeFromPackageJSON`/`restorePackageJSON`,
- assert the raw output bytes only differ from the input in the expected dependency line (e.g. via a line-by-line diff rather than a parsed-map comparison).

```
go test ./internal/cli/...
go test ./tests/... -run TestAdd
go test ./tests/... -run TestRemove
go test ./...
```
