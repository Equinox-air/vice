// cmd/vice/realworld.go
// Copyright(c) 2022-2026 vice contributors, licensed under the GNU Public License, Version 3.
// SPDX: GPL-3.0-only

package main

import (
	"strings"

	"github.com/AllenDang/cimgui-go/imgui"
	"github.com/mmp/vice/platform"
)

func uiShowRealWorldTrafficDialog(p platform.Platform, mode string) {
	uiShowModalDialog(NewModalDialogBox(&RealWorldTrafficModalClient{mode: mode}, p), true)
}

type RealWorldTrafficModalClient struct{ mode string }

func (c *RealWorldTrafficModalClient) Title() string {
	return "Real World Traffic - " + strings.Title(c.mode)
}
func (c *RealWorldTrafficModalClient) Opening() {}

func (c *RealWorldTrafficModalClient) Buttons() []ModalDialogButton {
	return []ModalDialogButton{{text: "OK"}}
}

func (c *RealWorldTrafficModalClient) Draw() int {
	imgui.Text("Real world traffic import is coming soon.")
	imgui.Spacing()
	imgui.Text("Selected mode: " + c.mode)
	return -1
}
