# Repository instructions

- Never commit Markdown documentation files from `readme_docs/` (`readme_docs/**/*.md`). They may be edited when requested, but must always remain outside Git commits.
- Do not write, modify, or run tests unless the user explicitly asks for it.
- `promo/promo.html` is the technical reference of the protocol and product internals. When a task changes anything it describes — wire format, packet types, handshake/crypto, server loop, voice pipeline, loss resilience, state sync, session lifecycle, chat, screen sharing, limits/constants, versions — update the affected sections in the same task. Keep it technical, cite source files, and do not leave stale numbers.
