// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
//
// Copyright (c) 2026 Omnipus contributors

package ui

import (
	"github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"
)

func (a *App) newHomePage() tview.Primitive {
	list := tview.NewList()
	list.SetBorder(true).
		SetTitle(" [#00f0ff::b] ACTIVE CONFIGURATION ").
		SetTitleColor(tcell.NewHexColor(0x00f0ff)).
		SetBorderColor(tcell.NewHexColor(0x00f0ff))
	list.SetMainTextColor(tcell.NewHexColor(0xe0e0e0))
	list.SetSecondaryTextColor(tcell.NewHexColor(0x808080))
	list.SetSelectedStyle(
		tcell.StyleDefault.Background(tcell.NewHexColor(0x39ff14)).Foreground(tcell.NewHexColor(0x050510)),
	)
	list.SetHighlightFullLine(true)
	list.SetBackgroundColor(tcell.NewHexColor(0x050510))

	rebuildList := func() {
		sel := list.GetCurrentItem()
		list.Clear()
		list.AddItem("MODEL: "+a.cfg.CurrentModelLabel(), "Select to configure AI model", 'm', func() {
			a.navigateTo("schemes", a.newSchemesPage())
		})
		list.AddItem(
			"CHANNELS: Configure communication channels",
			"Manage Telegram/Discord/WeChat channels",
			'n',
			func() {
				a.navigateTo("channels", a.newChannelsPage())
			},
		)
		list.AddItem("GATEWAY MANAGEMENT", "Manage Omnipus gateway daemon", 'g', func() {
			a.navigateTo("gateway", a.newGatewayPage())
		})
		// This used to shell out to the binary's old "agent" subcommand, which
		// was removed in the CLI redesign (cmd/omnipus/main.go::removedVerbs).
		// The child printed a "was removed" notice and exited 1, but its result
		// was assigned to _ , so pressing 'c' suspended the TUI, flashed an
		// error nobody could read, and returned as if nothing had happened.
		// There is no interactive CLI chat to point at — chat lives in the web
		// UI — so the item now says where it went instead of failing silently.
		list.AddItem("CHAT: Open the chat UI", "Chat moved to the web interface", 'c', func() {
			a.showInfo("CHAT IS IN THE WEB UI",
				"Interactive chat is no longer a CLI command.\n\n"+
					"Start the daemon from GATEWAY MANAGEMENT, then open\n"+
					"the web interface (http://localhost:5000 by default)\n"+
					"and chat there.")
		})
		list.AddItem("QUIT SYSTEM", "Exit Omnipus Launcher", 'q', func() { a.tapp.Stop() })
		if sel >= 0 && sel < list.GetItemCount() {
			list.SetCurrentItem(sel)
		}
	}
	rebuildList()

	a.pageRefreshFns["home"] = rebuildList

	return a.buildShell(
		"home",
		list,
		" [#00f0ff]m:[-] model  [#00f0ff]n:[-] channels  [#00f0ff]g:[-] gateway  [#00f0ff]c:[-] chat  [#ff2a2a]q:[-] quit ",
	)
}
