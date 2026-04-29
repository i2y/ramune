# vhs counter.tape → counter.gif
# Run: vhs counter.tape  (requires charmbracelet/vhs)

Output counter.gif
Set Shell "bash"
Set FontSize 14
Set Width 700
Set Height 400
Set Theme "Dracula"

Type "../../ramune run counter.tsx"
Enter
Sleep 800ms

Up
Sleep 250ms
Up
Sleep 250ms
Up
Sleep 400ms
Down
Sleep 400ms
Up
Sleep 400ms
Up
Sleep 600ms
Type "r"
Sleep 600ms
Type "q"
Sleep 400ms
