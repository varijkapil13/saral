# See docs/DEMO.md. Needs a binary built with -tags demo; the session is saral -fake.
Require saral

Output docs/media/demo.gif

# Frame: 120x36 cells. docs/DEMO.md says how to check that it still is.
Set Shell bash
Set FontFamily "JetBrains Mono"
Set FontSize 16
Set Padding 20
Set Width 1200
Set Height 740
Set Framerate 12
Set PlaybackSpeed 1
Set TypingSpeed 90ms
Set CursorBlink false
Set Theme "Catppuccin Frappe"

Hide
Type "clear"
Enter
Show

# The issue list
Type "saral -fake --theme dark"
Sleep 800ms
Enter
Sleep 4s
Type@250ms "jj"
Sleep 1500ms

# One issue whole: description, fields, thread
Enter
Sleep 3s
Screenshot docs/media/issue.png
Sleep 1s
Tab
Sleep 1500ms

# Transition it
Type "t"
Sleep 2s
Type "j"
Sleep 800ms
Enter
Sleep 1500ms
Type "y"
Sleep 3s
Escape
Sleep 2s

# The palette: commands and the issues already on disk, one list
Ctrl+K
Sleep 1500ms
Type@140ms "timel"
Sleep 1500ms
Enter
Sleep 3s

# The timeline, zoomed, saying where its dates came from
Type "+"
Sleep 1500ms
Type "n"
Sleep 2500ms
Type "n"
Sleep 1s

# The board, reached the way every root view is
Type@250ms "g2"
Sleep 4s
Screenshot docs/media/board.png
Sleep 1s

# The backlog: pick two issues and move them to the next sprint
Type@250ms "g3"
Sleep 3s
Type@300ms "j j"
Type@300ms "j "
Sleep 1s
Type "m"
Sleep 1500ms
Screenshot docs/media/bulk-move.png
Sleep 1s
Enter
Sleep 1500ms
Type "y"
Sleep 2500ms

# Releasing a version that still has open issues
Type@250ms "g5"
Sleep 3s
Type "j"
Sleep 800ms
Enter
Sleep 3s
Screenshot docs/media/release.png
Sleep 1s
Type "j"
Sleep 1500ms
Escape
Sleep 1500ms

Type "q"
Sleep 1s
