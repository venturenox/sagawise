# History

Frozen records of how the current design came to be. Read them for the reasoning behind a rule, never for current behaviour: the [architecture](../architecture.md) and the [contract](../contract.md) are the living documents, and where a page here disagrees with them, they win.

| Document | What it is |
| --- | --- |
| [Correctness audit](correctness-audit-2026-08-29.md) | The review of the original code base, August 2026: fifteen numbered findings (`#N`) about atomicity, timeout enforcement, swallowed errors and trust boundaries. Every later document cites findings by these numbers. |
| [Roadmap](TODO.md) | The ten-phase hardening plan that answered the audit, with each item ticked as it landed and a note on what was done. |
| [State-machine design](design-phase-6.md) | The design note for the rewrite of the engine core: document layout, one Lua script per transition, durable queues. Decisions P1 to P7. |

Pages here keep their original wording, including phase numbers and branch names.
