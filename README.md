# ⚡ Quicknote TUI

![Go Version](https://img.shields.io/badge/Go-1.20+-00ADD8?style=for-the-badge&logo=go)
![Bubble Tea](https://img.shields.io/badge/Built%20with-Bubble%20Tea-EE4C7C?style=for-the-badge)
![SQLite](https://img.shields.io/badge/SQLite-003B57?style=for-the-badge&logo=sqlite&logoColor=white)

A blazing fast, hyper-extensible terminal-based note-taking application built in Go. Quicknote isn't just a text box—it features a **custom 2D-rune rendering engine** that bypasses standard terminal limitations to deliver native physical/logical word-wrapping, syntax highlighting, inline math evaluation, and shell piping.

---

## ✨ Killer Features

* 🧠 **Custom Rendering Engine:** Infinite logical line lengths, Unicode-safe cursor math, and perfect physical wrapping without breaking syntax highlighting.
* 🎨 **Real-Time Syntax Highlighting:** Powered by Chroma. Supports Markdown out of the box, with dedicated `math:` and `code:` modes.
* 🧮 **Inline Math & Conversions:** Type a math equation or conversion (e.g., `100 c to f` or `sqrt(64)`) and press `=` to instantly evaluate it inline!
* ⏱️ **#kill Timers:** Ephemeral notes that auto-delete. Add `#kill 2 days` or `#kill 30 mins` to a note, and watch the live countdown in your status bar.
* 👻 **Ghost Mode:** Press `Ctrl+G` to detach a note from the SQLite database. It lives in memory until you close the app, leaving zero trace.
* 🐚 **Shell Piping:** Press `Alt+X` to pipe the contents of your note directly through any shell command (e.g., `jq`, `grep`, `sort`) and instantly replace the buffer with the output.
* 🪄 **Magic Keywords:** Type `:today:`, `:time:`, or even `:now + 3 weeks:` and watch it instantly expand into perfectly formatted timestamps.
* 📝 **Smart Lists:** Automatic indentation and sequence correction for numbers, letters, and Roman numerals. 

---

## 🚀 Installation

Ensure you have Go installed on your machine, then run:

```bash
go install https://github.com/reeshadarian/quicknote-tui
```

All notes are stored safely locally in a SQLite database located at `~/.local/share/quicknote-tui/notes.sqlite`.

---

## 💻 CLI Usage

Quicknote is designed to integrate seamlessly into your terminal workflow. You can pipe data directly into it, or use flags for rapid background saves.

```bash
# Open the TUI normally
quicknote

# Pipe command output directly into a new note
ls -la | quicknote

# Create a note directly from the command line without opening the TUI
quicknote -n "Remember to buy milk" -q

# Append text to an existing note titled "Groceries"
quicknote -t "Groceries" -n "Eggs" -q

# Append to your most recently edited note
quicknote -l -n "Just one more thing..." -q

# Execute a shell command and capture its output into a new note named after the command!
quicknote -c "ping -c 4 google.com"
```

**CLI Flags:**
* `-n`: The text content of the note to create/append.
* `-q`: Quiet mode. Saves the note and exits without launching the UI.
* `-t`: Title. Searches for a note with this heading and appends to it. If it doesn't exist, creates a new note with this title.
* `-l`: Last. Appends the text to the most recently edited note.
* `-c`: Command. Executes a shell command and captures the output.

---

## ⌨️ TUI Cheatsheet

### Global Navigation
| Keybinding | Action |
| :--- | :--- |
| `Alt+Left` / `Alt+Right` | Switch between notes |
| `Ctrl+U` | Promote current note to the front of the deck |
| `Ctrl+N` | Create a new, blank note |
| `Ctrl+D` | Delete the current note (moves to Trash) |
| `Ctrl+S` | Export current note to a local `.md` file |
| `Alt+C` | Copy entire note to system clipboard |
| `Ctrl+F` | Open Search / Find interface |
| `Ctrl+T` | Open the Trash bin |
| `F1` | Toggle Help & Stats screen |

### Editor & Formatting
| Keybinding | Action |
| :--- | :--- |
| `F2` | Toggle Line Numbers |
| `Ctrl+B` | Insert Bold markdown (`****`) |
| `Alt+I` | Insert Italic markdown (`**`) |
| `Ctrl+X` | Cycle Checkboxes (`- [ ]` → `- [x]` → `~~strikethrough~~`) |
| `Ctrl+O` / `Alt+Enter` | Smart Open line below (maintains indentation) |
| `Ctrl+K` | Delete entire current line |
| `Tab` / `Shift+Tab` | Indent / Un-indent (4 spaces) |
| `\`` | Auto-pair inline code. Press 3x for multiline block |
| `Ctrl+Z` / `Alt+Z` | Undo |
| `Ctrl+Y` / `Alt+Y` | Redo |

### Special Modes
| Keybinding | Action |
| :--- | :--- |
| `Alt+M` | Toggle **Math Mode** (Prepends `math:` to doc) |
| `Alt+V` | Toggle **Code Mode** (Prepends `code:` to doc, disables smart lists) |
| `Ctrl+G` | Toggle **Ghost Mode** (Ephemeral / Unsaved) |
| `Alt+X` | Open **Pipe Prompt** (Filter text through shell commands) |
| `=` | Evaluate math/conversion on the current line |

---

## 🔮 Deep Dive: Magic Features

### Inline Math & Conversions
Type an expression and press `=`. Quicknote will evaluate it instantly and append the result.
* **Basic Math:** `25 * 4 = 100`
* **Constants & Functions:** `sqrt(pi * 10) = 5.604991`
* **Percentages:** `20% of 500 = 100` or `150 + 10% = 165`
* **Unit Conversions:** `5 kg to lbs = 11.023100`
* **Temp Conversions:** `100 c to f = 212`
* **Base Conversions:** `255 dec to hex = FF`

*(Note: Add `//` to the start of a line to comment it out and protect it from math parsing).*

### Magic Keywords (Date Math)
Type these exact tags and type a `:` to trigger expansion:
* `:today:` → `17 Apr 2026`
* `:time:` → `10:45PM`
* `:now:` → `10:45PM 17 Apr 2026`
* **With Math!** `:today + 2 weeks:` → `01 May 2026`
* **With Math!** `:now - 5 hrs:` → `5:45PM 17 Apr 2026`

### The Kill Timer
Need to keep a secret or temporary note? Type `#kill <number> <unit>` anywhere in your document:
* `#kill 10 mins`
* `#kill 2 days`
* `#kill 1 month`

The status bar will turn red and display a live countdown. When the timer hits zero, the note is automatically sent to the Trash bin (or permanently destroyed if Ghost Mode is active).

### Shell Piping
Press `Alt+X`. Type a command like `jq .` or `sort -r`. Quicknote will take the current body of your note, pipe it through your local shell environment, and replace the text with the formatted output. It acts as a shield, intentionally ignoring meta headers like `# Title`, `math:`, or `code:`, so they aren't destroyed by your pipe.

---

## 🏗️ Architecture

Quicknote uses a completely custom 2D buffer (`[][]rune`) replacing the standard Bubble Tea `textarea`. 

This enables:
* **O(1) Vertical Navigation:** Arrow keys move mathematically across a logical coordinate grid.
* **Intelligent Reflow:** Uses `muesli/reflow/wordwrap` to dynamically map physical wrap offsets back to the logical 2D grid, mimicking VS Code/Vim cursor memory.
* **Perfect Syntax Preservation:** Colors (ANSI codes) are scraped, buffered, and re-injected dynamically across physical line wraps, preventing formatting bleed or premature color drops. 

---
*Built with ❤️ using Charm.*


