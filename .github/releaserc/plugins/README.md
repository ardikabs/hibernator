# Semantic Release Scope Filter Plugin

> Inspired from https://github.com/joe-p/semantic-release-scope-filter

A local CommonJS plugin for semantic-release that:

- ✅ Filters commits by scope
- ✅ Supports both include and exclude logic
- ✅ Always skips commits containing:

    ```bash
    [skip release]
    [skip ci]
    ```

- ✅ Works with .releaserc.yml
- ✅ Compatible with ESM projects (by using .cjs)

## ⚙️ Configuration (.releaserc.yml)

```yaml
plugins:
  - - ./.github/releaserc/plugins/scope-filter.cjs
    - scopes:
        - core
        - api
      excludeScopes:
        - charts
        - release
      filterOutMissingScope: false

  - - "@semantic-release/commit-analyzer"
    - preset: conventionalcommits

  - - "@semantic-release/release-notes-generator"
    - preset: conventionalcommits
```

> ⚠️ The scope-filter plugin must come before commit-analyzer.

## 🏷️ Scope Detection

Scopes are extracted from Conventional Commit headers:

```bash
type(scope): message
```

Example:

```bash
feat(core): add feature
fix(api): correct bug
chore: update deps
```

If no scope is present:

```bash
feat: add feature
```

The scope is treated as (empty string) and will be filtered based on `filterOutMissingScope` setting:

```
""
```

## 🔧 Configuration Options

- `scopes` (Array | null)

  Allowlist of scopes.
  If defined and non-empty, only those scopes are allowed.

  Example:

  ```yaml
  scopes:
  - core
  - api
  ```

  If omitted or empty → no inclusion filtering is applied.

- `excludeScopes` (Array)

  Blocklist of scopes.
  Example:

  ```yaml
  excludeScopes:
  - charts
  - release
  ```

  Exclusion always has priority over inclusion.

- `filterOutMissingScope` (Boolean)

  Controls whether commits without scope are allowed.

  | Value             | Behavior                         |
  | ----------------- | -------------------------------- |
  | `false` (default) | Allow commits without `(scope)`  |
  | `true`            | Reject commits without `(scope)` |

  ⚠️ Important: This option applies ONLY when scopes (inclusion filter) is active.

  It has no effect if scopes is:

  - `null`
  - undefined
  - empty array (`[]`)