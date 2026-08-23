# Aimee Config Module

This repository owns Aimee's complete configuration implementation: its
pure-Go YAML store, defaults, validation, versioned and atomic mutations,
language-neutral event contract, and event-bus process. Aimee callers retain
only client-side accessors and contract bindings; no configuration parser or
storage implementation is compiled from the Aimee repository.

It builds `aimee-module-config` as a pure-Go process for the
server, kb bus. The exported repository includes the
exact canonical Go bus client/runtime snapshot, caller contract, handler,
validation, and persistent store. It contains no C implementation or bridge.

Before a daemon bus exists, container bootstrap code may invoke
`aimee-module-config --get PUBLIC_STRING_KEY`. This deliberately narrow mode is
implemented by the same Go store, refuses secrets and non-string values, and
prevents bootstrap scripts from becoming a second YAML parser.
The `--snapshot` form exposes the same secret-filtered document for native
contract-test fixtures; production callers use the event bus.

The daemon admits the process only when its installed absolute executable path,
UID, principal class, principal reference, and event-kind grants match the
installed `.grant` file. Copy that generated grant into each declared daemon
policy directory under `modules.d`.

The descriptor-owned production sources, tests, contracts,
and documentation are preserved at their canonical paths so their migration
history remains auditable.
