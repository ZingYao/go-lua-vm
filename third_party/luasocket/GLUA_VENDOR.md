# LuaSocket Vendor Record

## Source

- Project: LuaSocket
- Upstream: `https://github.com/lunarmodules/luasocket`
- Version/tag: `v3.1.0`
- Source commit: `95b7efa9da506ef968c1347edf3fc56370f0deed`

## License

- License text is preserved in `LICENSE`.
- Copyright holder: Diego Nehab.
- License family: MIT-style permissive license.

## Local Changes

- The upstream `v3.1.0` source snapshot was copied into `third_party/luasocket/`.
- No source files were modified.
- This directory is intended for `native_modules` compatibility validation only; build and test scripts must use the repository's Lua 5.3 public headers from `native/lua53/include/` instead of system Lua development packages.
- Runtime network acceptance remains a separate TODO because it depends on current platform socket behavior and Linux/macOS/Windows native build closure.
