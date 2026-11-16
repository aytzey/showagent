# Sample-data illustration. No real agent or model is invoked.
# Build demo/.build/showagent first; see demo/README.md.
Output docs/demo.gif
Output docs/demo.mp4

Set Shell bash
Set FontSize 20
Set Width 1200
Set Height 700
Set Padding 12
Set Theme "Catppuccin Mocha"
Set TypingSpeed 35ms

Hide
Type "exec bash demo/run-isolated.sh --shell"
Enter
Sleep 1s
Type `printf '\033[2J\033[H\n  \033[1mCodex -> Claude. Bring the conversation.\033[0m\n\n  Sample sessions; simulated CLI output.\n\n'`
Enter
Show
Sleep 2.5s
Type "showagent"
Enter
Sleep 2.5s

# The newest fixture is the Codex rate-limit session. x asks where the new
# session should pick up: the end, or any user message (newest first).
Type "x"
Sleep 1.8s
Down
Sleep 900ms
Up
Sleep 700ms

# enter keeps the whole conversation and shows the hand-off before any write.
Enter
Sleep 4s

# The second x writes the Claude session and selects it.
Type "x"
Sleep 2.5s

# A new native-format row; resume reaches a labeled illustration stub.
Enter
Sleep 3.5s
Hide
Type `printf '\033[2J\033[H\n  \033[1mSwitch coding agents. Bring the conversation.\033[0m\n\n  brew install aytzey/tap/showagent\n\n  github.com/aytzey/showagent\n\n  Sample sessions; simulated CLI output.\n'`
Enter
Show
Sleep 4.5s
