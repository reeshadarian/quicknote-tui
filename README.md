<div align="center">
  <h1>Quicknote</h1>
  <p><b>A blazing fast, hyper-extensible terminal scratchpad.</b></p>

  [![Go Version](https://img.shields.io/badge/Go-1.20+-00ADD8?style=flat-square&logo=go)](https://go.dev/)
  [![Built with Bubble Tea](https://img.shields.io/badge/UI-Bubble%20Tea-EE4C7C?style=flat-square)](https://github.com/charmbracelet/bubbletea)
  [![SQLite Backend](https://img.shields.io/badge/Backend-SQLite%20FTS5-003B57?style=flat-square&logo=sqlite&logoColor=white)](https://sqlite.org/)
  [![License: MIT](https://img.shields.io/badge/License-MIT-yellow.svg?style=flat-square)](https://opensource.org/licenses/MIT)
</div>

---

Quicknote isn't just a text box—it is a secure, ephemeral knowledge buffer for terminal power users. Built entirely in Go, it features a **custom 2D-rune rendering engine** that bypasses standard terminal limitations to deliver sub-millisecond startup times, native word-wrapping, real-time syntax highlighting, inline math evaluation, and direct shell piping.

> [!NOTE]
> **Why another note app?** Quicknote is optimized for *ephemerality*. It is designed for the things you don't want polluting your main knowledge base: temporary JSON blobs, quick server IPs, running logs, and one-off terminal command outputs.

## Core Features

- **Instant Startup:** Achieves sub-millisecond load times using Memory-Mapped SQLite and lazy-loaded UIs.
- **Advanced Fuzzy Finder:** Powered by SQLite FTS5. Instantly filter through thousands of notes with exact-line previews and cursor-jumping.
- **Time-to-Live (TTL):** Append `#kill 2 days` to any note to securely self-destruct it when the timer expires.
- **Smart Trash:** Deleted notes live for 3 days. The database automatically deduplicates content to save space.
- **Unix Philosophy:** Pipes seamlessly with standard input (`stdin`) and output (`stdout`).

---

## ⚡ Command Line Interface (CLI)

Quicknote is built to be a silent companion in your shell workflow. You can interact with your notes without ever opening the UI.

### Standard Usage
```bash
# Open the Quicknote TUI (defaults to your most recent active note)
$ quicknote

# Create a new note directly from the terminal
$ quicknote -n "Check the database migrations on staging."

# Execute a command, capture the output, and save it as a new note
$ quicknote -c "ls -la /var/log"
```

### Piping Data (Stdin)
Quicknote smoothly ingests standard output from other Unix tools.
```bash
# Save a server error log to a quick note titled "Panic Log"
$ cat error.log | grep "panic" | quicknote -t "Panic Log"

# Save a curled JSON payload quietly in the background
$ curl [https://api.github.com/users/torvalds](https://api.github.com/users/torvalds) | quicknote -q
```

<details>
<summary><b>View all CLI Flags</b></summary>

| Flag | Description |
| :--- | :--- |
| `-n` | Create a note directly from the command line text. |
| `-q` | Quiet mode: Save the note and exit without opening the UI. |
| `-t` | Title/Heading for the note (creates a new note or appends to an existing one). |
| `-l` | Append the input to your most recently used active note. |
| `-c` | Execute a shell command, capture the output, and use the command as the title. |

</details>

---

## ⌨️ TUI Keyboard Shortcuts

Quicknote is designed to keep your hands on the keyboard. 

| Shortcut | Action | Description |
| :--- | :--- | :--- |
| <kbd>Ctrl</kbd> + <kbd>F</kbd> | **Fuzzy Find** | Opens the floating search modal. Searches across all active notes. |
| <kbd>Alt</kbd> + <kbd>X</kbd> | **Shell Pipe** | Pipe the current note through a shell command (e.g., `jq .`). |
| <kbd>Ctrl</kbd> + <kbd>G</kbd> | **Ghost Mode** | Detach the note from the database. It will permanently delete upon exit. |
| <kbd>=</kbd> | **Eval Math** | While at the end of an equation (e.g., `sqrt(64)`), evaluates the math inline. |
| <kbd>Esc</kbd> | **Normal Mode** | Exit search/menus and return to typing. |

> [!TIP]
> **Searching the Trash:** If you open the Trash bin and press <kbd>Ctrl</kbd> + <kbd>F</kbd>, the fuzzy finder will automatically restrict its search to only your deleted notes.

---

## 🔬 Deep Dive: Advanced Capabilities

### 1. The FTS5 Fuzzy Finder
Pressing <kbd>Ctrl</kbd> + <kbd>F</kbd> opens a centered modal that searches your database as you type. 
- It uses SQLite's Full-Text Search (FTS5) to find exact keyword matches across all files instantly.
- The preview window strips out noise and shows you the exact line where your match occurred.
- Pressing <kbd>Enter</kbd> will instantly load the note and **jump your cursor to the exact row and column** of the matched word.

### 2. Time-to-Live (Kill Timers)
Need to store an API key, a temporary password, or a quick reminder? Type a `#kill` tag anywhere in your document:

```markdown
Here is the temporary staging password:
admin_12345
#kill 15 mins
```
The status bar will immediately turn red and display a live countdown. When the timer hits zero, the note is automatically sent to the Trash bin.
*Supported units: `mins`, `hours`, `days`, `months`.*

### 3. Inline Math & Unit Conversions
Quicknote can act as a scratchpad calculator. Type a mathematical equation or conversion, and press <kbd>=</kbd> to instantly evaluate it.

```text
500 * 0.15          -> (Press =) -> 75
100 c to f          -> (Press =) -> 212
sqrt(64) + 10       -> (Press =) -> 18
```

### 4. In-App Shell Piping
Press <kbd>Alt</kbd> + <kbd>X</kbd> to open the Pipe prompt. You can pass the entire contents of your current note through your local shell environment and replace the text with the formatted output. 
- **Example:** Paste an unformatted JSON block into a note, press `Alt+X`, type `jq .`, and the note will instantly update with the pretty-printed JSON.
- Quicknote intelligently ignores Markdown headers and metadata tags during piping so your titles are never destroyed.

---

## 🏗️ Architecture

Quicknote achieves its speed and flexibility by discarding the standard Bubble Tea `textarea` component in favor of a **completely custom 2D buffer (`[][]rune`)**.

- **O(1) Vertical Navigation:** Arrow keys move mathematically across a logical coordinate grid, preserving your physical cursor position even on wrapped lines.
- **Intelligent Reflow:** Uses `muesli/reflow/wordwrap` to dynamically map physical wrap offsets back to the logical 2D grid, mimicking native VS Code/Vim cursor memory.
- **Persistent Indexing:** Uses SQLite Triggers to maintain a virtual search table (`notes_fts`) that updates asynchronously, ensuring 0ms blocking on the UI thread when autosaving.

---

## 📥 Installation

Ensure you have Go 1.20+ installed.

```bash
git clone [https://github.com/yourusername/quicknote-tui.git](https://github.com/yourusername/quicknote-tui.git)
cd quicknote-tui
go build -ldflags="-s -w" -o quicknote .
sudo mv quicknote /usr/local/bin/
```

Quicknote will automatically create its lightweight SQLite database in `~/.local/share/quicknote-tui/` on first run.

---
<div align="center">
  <i>Built with ❤️ for terminal power users.</i>
</div>
