# lua-cjson vendoring record

- Upstream: `https://github.com/mpx/lua-cjson`
- Version/tag: `2.1.0`
- Source commit: `4bc5e917c8cd5fc2f6b217512ef530007529322f`
- License: MIT-style license, preserved in `LICENSE`.
- Local modifications: none. This directory is a source snapshot for native module compatibility validation.
- Intended use: compile-time and runtime validation for the optional `native_modules` build. The default no-CGO build must not depend on this source tree.

The source is intentionally committed to the repository so native module acceptance tests do not download third-party code at test time.
