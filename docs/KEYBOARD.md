# Keyboard profiles (configuration version 1)

Default v2: **Ctrl+K Commands**, **Alt+K delete line**. Legacy: **Ctrl+K delete line**, **Alt+P Commands**. Other established shortcuts remain available. Help and the basic Commands menu display the active bindings. The full fuzzy palette is a later task (QN-023).

Use the Commands action **Switch keyboard profile** to choose the other profile. Changes are saved atomically to `~/.config/quicknote-tui/keyboard.json`. A failed save leaves the current profile active and reports the failure.

Custom bindings use stable action IDs:

```json
{
  "version": 1,
  "profile": "default",
  "bindings": {
    "note.new": ["alt+n"],
    "commands.open": ["ctrl+p"]
  }
}
```

Restart to load manually edited configuration. Each override replaces that action's previous keys. Unknown profiles/versions/actions, invalid keys, and collisions within an active/modal context produce a visible error and a safe default fallback. Literal typing keys cannot be reassigned to global editor commands. Context-specific aliases (e.g. search Ctrl+K) do not conflict with editor shortcuts.

Terminal/tmux interception can prevent keys from reaching Quicknote. Ctrl+Shift combinations are often indistinguishable from Ctrl; this version rejects unsupported key spellings rather than promising they work. Use tested Ctrl-letter, Alt-letter, arrows/navigation and function keys. All menus support keyboard cancel without changing the draft.
