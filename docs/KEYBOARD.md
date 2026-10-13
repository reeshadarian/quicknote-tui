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

Expiration is available as **Set expiration** in Commands, **Alt+T** in both profiles, and a click on the fully visible TTL/countdown status label. Override `"ttl.set": ["alt+e"]` to choose another shortcut. Ghosts and Trash explain why expiration is unavailable; save a new scratch draft first. Pinned notes may choose Never; unpin before selecting a finite timer.

Use arrows to select 15 minutes, 1 hour, Today, 3 days, 7 days, Never, or Custom, then Enter to review. The review shows an absolute local date/time with zone and numeric UTC offset; it defaults to **Cancel**. Tab selects Confirm, then Enter applies. Relative choices begin at this final confirmation. Esc closes one layer and preserves the draft, history and custom input.

Custom accepts positive elapsed durations such as `15m`, `1h30m` or `3d`, an RFC3339 timestamp with an explicit offset (for example `2026-11-01T01:30:00-07:00`), or a unique local `YYYY-MM-DD HH:MM`. Local times in a daylight-saving gap or fold require an explicit offset. Past/equal deadlines, overflow and trailing junk are rejected. Today means local 23:59:59; fixed days mean elapsed 24-hour periods. The countdown reads the saved deadline and reflects clock changes on the next tick.

Picker actions (`ttl.cancel`, `ttl.up`, `ttl.down`, `ttl.review`) support validated bindings; cancel must retain Esc. Confirmation controls remain fixed. The status always puts the save result before transient messages; a clipped or hidden label cannot receive clicks. Extremely small terminals preserve keyboard routes even when they cannot display a TTL label.
