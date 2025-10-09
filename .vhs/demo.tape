Output demo.gif

Set Theme "Catppuccin Frappe"
Set FontSize 46
Set Width 2400
Set Height 1200

Type "mkdir -p demo-repos; cd demo-repos"

Enter
Sleep 1s

Type "git clone git@github.com:blankstatic/syncerd.git"
Enter
Sleep 3s

Type "git clone git@github.com:blankstatic/autogitpull.git"
Enter
Sleep 3s

# Discover repositories
Type "autogitpull discover ."
Enter
Sleep 2s

Enter

Type "autogitpull status"
Enter
Sleep 2s

Sleep 300ms

Down

Sleep 300ms

Type "p"

Sleep 5s

Type "q"
Enter

Sleep 1s
