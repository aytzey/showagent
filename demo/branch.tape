# Sample-data illustration. No real agent or model is invoked.
# Build demo/.build/showagent first; see demo/README.md.
Output docs/branch.gif

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
Type `printf '\033[2J\033[H\n  \033[1mBranch from any message. Keep the original.\033[0m\n\n  Sample sessions; simulated CLI output.\n\n'`
Enter
Show
Sleep 1.5s
Type "showagent"
Enter
Sleep 1.5s

# The newest fixture is the Codex rate-limit session. n opens the branch
# chooser with the end and every user message, newest first.
Type "n"
Sleep 2s
Type "/"
Sleep 400ms
Type "sliding"
Sleep 1.2s
Enter
Sleep 900ms

# Search selects user #4. Tab leaves the wrong turn out, keeping the reply
# before it so the copied session can take a different direction.
Tab
Sleep 3s
Enter
Sleep 3s

# The new Codex row is selected; resume reaches a labeled illustration stub.
Enter
Sleep 2.5s
Hide
Type `printf '\033[2J\033[H\n  \033[1mBranch from any message. Keep the original.\033[0m\n\n  brew install aytzey/tap/showagent\n\n  github.com/aytzey/showagent\n\n  Sample sessions; simulated CLI output.\n'`
Enter
Show
Sleep 4s
