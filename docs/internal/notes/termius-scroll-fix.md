# Termius (iPad) + Grok: why text disappears

## What is happening

Termius on iPad often **cannot scroll inside full-screen apps** (Grok TUI, `less`,
`vim`, sometimes even `tmux` panes that use the alternate screen). When a line
moves above the visible area, it is **not kept** — it is gone.

That is an app/buffer limitation, not you missing a Grok key.

## 30-second test (plain shell, NOT inside grok)

```bash
seq 1 80
```

Then try **two-finger swipe up** on the terminal area.

| Result | Meaning |
|--------|---------|
| You can see earlier numbers (1, 2, 3…) | Termius scrollback works for normal output. Only Grok/TUI is broken. |
| Numbers are gone forever | Termius scrollback is off or broken — fix Termius first (below). |

## Fix Termius scrollback (if the test fails)

In Termius (wording varies by version):

1. Open the **host / terminal settings** for this connection  
2. Find **Scrollback** / **Terminal** / **Appearance**  
3. Set scrollback to a large value (e.g. **10000** lines), not 0  
4. Ensure you are using a **full Terminal** session, not a tiny widget  
5. Scroll gesture: **two fingers** on the terminal (one finger is often selection)

Also try: rotate to landscape (more lines) so less content is “lost” per reply.

## If the test works but Grok still loses text

Grok draws a **TUI / alternate screen**. Termius cannot scroll that buffer.
Do **not** try to read long answers inside Grok.

**Always read long content as files:**

```bash
# Interactive pager that only needs Enter (no scroll gesture)
bash docs/internal/notes/read-pages.sh

# Or one page at a time:
cat docs/internal/notes/security-ux-pages/page-00.txt
cat docs/internal/notes/security-ux-pages/page-01.txt
# …
```

## tmux (if you use it)

Two-finger scroll often does nothing. Keyboard only:

1. `Ctrl+b` then `[`  → copy mode  
2. Arrow keys / `Ctrl+u` / `Ctrl+d` if those work  
3. `q` to exit  

Prefer a **second Termius tab** with a plain shell for reading files.

## Best workflow on iPad + Termius

| Tab | Use for |
|-----|---------|
| 1 | `grok` — short prompts only |
| 2 | `bash docs/internal/notes/read-pages.sh` or `cat page-NN.txt` |

Ask Grok: “write the answer to docs/internal/notes/latest.md in short pages”
then read tab 2.
