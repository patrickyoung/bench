# Development guidance

`bench` is the terminal workbench for the small bench filters. It is a
control plane, never a second implementation of a filter.

When changing `bench`:

- invoke `ask`, `brief`, `ply`, and the rest as ordinary programs; do not
  import their internals or duplicate their policy;
- keep every boundary inspectable: arguments are argv, data is stdin,
  answers are stdout, progress is stderr, and outcome is the exit status;
- use explicit session and artifact paths. The TUI may render derived state,
  but an ordinary command must be able to inspect the durable source;
- never run user text through a shell. Shell syntax is data unless the user
  has explicitly chosen a shell-backed check;
- do not claim success from model prose. A successful build or evaluation is
  a check that exited zero;
- keep the interface usable at 80x24, without colour, and with the mouse
  unplugged;
- test the process boundary with a fake executable and the interface as a
  state machine; tests must not require a model, credentials, or a network;
- run `go test ./...` before reporting success.

The implemented product boundary is deliberately compositional: `ask`
conversation, `draft new/check/build/prove`, and `hone -into` are separate
programs with visible evidence and exit-status verdicts. Further build,
evaluation, and learning features belong here only as real compositions of
existing filters, added one verified vertical slice at a time.
