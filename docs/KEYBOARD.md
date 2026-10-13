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

While an expiration write is pending, another explicitly confirmed choice is queued and applied after the first acknowledgment. The latest confirmed choice wins; edits or switching away before queued work starts cancel its source intent. Custom supports terminal bracketed paste, and its field scrolls horizontally to keep the cursor visible when resized.

Trash is a read-only preview. **Ctrl+T** opens it; **Up/Down/PageUp/PageDown** scroll; **Ctrl+F** searches Trash and opens read-only results. The preview shows deletion reason, deletion time, and the 72-hour recovery deadline. **Ctrl+R** or Commands **Restore note** opens choices with **Cancel** selected. Tab/Left/Right selects **Restore without timer**, or **Restore and reset timer** when a validated directive timer is remembered. Enter accepts the selected choice. Pinned notes restore with no timer; unchanged historical `#kill` text does not restart a canceled timer.

Commands **Undo last move to Trash** restores the latest successful manual deletion's original ID without a timer, including a ghost conversion that moved its original to Trash. It is available in the editor and Trash, with an explanation when unavailable. **Ctrl+Z remains editor text undo**. Undo retains note history and returns to the restored note if the initiating draft is still clean and unchanged. A remote restore/edit or purge cannot be overwritten by Undo. Recovery includes exactly 72 elapsed hours; after that the action is unavailable.

Commands **Delete forever** is available only for a selected Trash entry, separately confirms with **Cancel** selected, and labels the irreversible choice **Delete forever**. Failure keeps the selection and shows a retryable error. There is no bulk permanent-delete action. Normal **Ctrl+D** now moves even empty persisted notes to recoverable Trash; unsaved drafts and ghosts are discarded from memory without creating Trash. If an unsaved draft's first save is already running, deletion waits for its assigned ID and then moves that persisted note to Trash.

These actions work in default and legacy profiles and accept custom bindings for `note.restore`, `trash.undoLast`, and `trash.deleteForever`. For example, assign `"trash.undoLast": ["alt+u"]` and `"trash.deleteForever": ["alt+d"]`. Commands remains Ctrl+K in default and Alt+P in legacy. No lifecycle action promises secure erasure of prior disk copies or backups.
