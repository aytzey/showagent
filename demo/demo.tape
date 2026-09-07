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
Type `printf '\033[2J\033[H\n  Codex -> Claude. Bring the conversation.\n\n  Sample sessions; simulated CLI output.\n\n'`
Enter
Show
Sleep 3s
Type "showagent"
Enter
Sleep 1.5s

# The newest fixture is the Codex rate-limit session. Show the handoff early.
Type "x"
Sleep 4.5s
Type "x"
Sleep 2s

# A new native-format row; resume reaches a labeled illustration stub.
Enter
Sleep 3.5s
Hide
Type `printf '\033[2J\033[H\n  Switch coding agents. Bring the conversation.\n\n  brew install aytzey/tap/showagent\n\n  github.com/aytzey/showagent\n\n  Sample sessions; simulated CLI output.\n'`
Enter
Show
Sleep 5s
