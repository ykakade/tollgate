# Demo recording

`make demo-gif` records `docs/assets/demo.gif` with fake providers. It needs no provider key.

Prerequisites:

- [VHS](https://github.com/charmbracelet/vhs), with `ttyd` and `ffmpeg`
- [jq](https://jqlang.org)
- Claude Code
- Chrome or Chromium. Set `CHROME` to its path if the command is not `chromium`.
- The ports 8080, 8091, 8092 and 9090 on `127.0.0.1` are free.

Run:

```sh
make demo-gif CHROME=google-chrome
```

Result: `record.sh` starts a failing fake provider, a working fake provider and Tollgate. VHS records `demo.tape`: Claude Code sends one request, and Tollgate sends it to the backup. Then Chrome takes a screenshot of the dashboard, and ffmpeg adds it to the end of the GIF.

The recording uses a new Claude Code configuration directory, so it shows no local login or settings.
