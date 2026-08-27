# See docs/DEMO.md before recording: this tape needs a profile it cannot create.
Require saral

Output demo.gif

# Frame: 120x36 cells. docs/DEMO.md says how to check that it still is.
Set Shell bash
Set FontFamily "JetBrains Mono"
Set FontSize 16
Set Padding 20
Set Width 1200
Set Height 740
Set Framerate 30
Set PlaybackSpeed 1
Set TypingSpeed 90ms
Set CursorBlink false
Set Theme "Catppuccin Frappe"

# The scratch profile, which docs/DEMO.md specifies.
Env SARAL_CONFIG_DIR "/tmp/saral-demo/config"
Env SARAL_CACHE_DIR "/tmp/saral-demo/cache"
Env SARAL_DEMO_PROJECT "EX"

Hide
Type "clear"
Enter
Show

# The issue list
Type "saral --project $SARAL_DEMO_PROJECT --theme dark"
Sleep 800ms
Enter
Sleep 4s
Type@250ms "jj"
Sleep 1500ms

# One issue whole: description, fields, thread
Enter
Sleep 3s
Tab
Sleep 1500ms

# Transition it
Type "t"
Sleep 2s
Type "j"
Sleep 800ms
Enter
Sleep 3s
Escape
Sleep 1500ms
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

Type "q"
Sleep 1s
