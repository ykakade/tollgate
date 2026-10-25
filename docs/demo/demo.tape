# The terminal part of docs/assets/demo.gif. Run "make demo-gif" to record
# it: docs/demo/record.sh starts the fake providers and Tollgate first.

Output demo-terminal.gif

Require claude
Require jq

Set Shell bash
Set FontSize 18
Set Width 1200
Set Height 640
Set Padding 24
Set Theme "Catppuccin Mocha"
Set TypingSpeed 35ms
Set WaitTimeout 30s

Hide
Type `cd "$TOLLGATE_DEMO_DIR" && PS1='\[\e[1;35m\]$ \[\e[0m\]' && clear`
Enter
Show

Type "# Claude Code sends its requests to Tollgate. The primary provider fails with 529."
Enter
Sleep 500ms
Type `claude -p "Say hello"`
Enter
Wait+Screen /fake provider\./
Sleep 1.5s

Type "# Tollgate tried the primary, then sent the request to the backup:"
Enter
Sleep 300ms
Type `tail -n 2 tollgate.log | jq -c '{msg, provider, reason, attempts, cost_micro_usd} | del(..|nulls)'`
Enter
Sleep 3.5s

Type "# The spend is in the dashboard at http://127.0.0.1:9090"
Enter
Sleep 2s
