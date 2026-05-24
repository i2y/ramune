# agentboard — interactive demo recording.
#   vhs demo.tape
# Paths are absolute; adjust for your machine. Uses the mock agent so the
# recording is fast, deterministic, and token-free.
Output "/Users/i2y/esffi/examples/agentboard/demo.gif"
Set Shell "bash"
Set FontSize 13
Set Width 1000
Set Height 540
Set Theme "Dracula"

# Launch off-camera so the GIF opens on the live board.
Hide
Type "AGENTBOARD_REPO=/tmp/ab-demo AGENTBOARD_AGENT=mock /Users/i2y/esffi/ramune run /Users/i2y/esffi/examples/agentboard/agentboard.ts"
Enter
Sleep 5s
Show

Sleep 1200ms

# Create two task cards.
Type "n"
Sleep 500ms
Type "add an MIT LICENSE file"
Sleep 700ms
Enter
Sleep 700ms

Type "n"
Sleep 500ms
Type "write a project CHANGELOG"
Sleep 800ms
Enter
Sleep 1s

# Start both — each agent runs in its own git worktree, in parallel.
Enter
Sleep 1300ms
Enter
Sleep 4500ms

# Jump to the Review column, open a card's diff.
Right
Sleep 300ms
Right
Sleep 800ms
Enter
Sleep 3s
Escape
Sleep 900ms

# Merge it — the card lands in Done.
Type "m"
Sleep 2200ms

Type "q"
Sleep 800ms
