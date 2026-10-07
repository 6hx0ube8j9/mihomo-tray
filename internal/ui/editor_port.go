package ui

import (
	"errors"
	"strconv"

	"github.com/tailscale/walk"
	. "github.com/tailscale/walk/declarative"
	"mihomo-tray/internal/netutil"
)

func (e *Engine) ShowPortEditor(defaultMixed, defaultSocks, defaultHttp int) (int, int, int, bool) {
	if e.dialogMgr == nil {
		return 0, 0, 0, false
	}

	var finalMixed, finalSocks, finalHttp int
	var isAccepted bool

	e.dialogMgr.RunOnUI(func() {
		key := "editor_port"
		if !e.dialogMgr.TryAcquire(key) {
			return
		}
		defer e.dialogMgr.Release(key)

		type portField struct {
			name   string
			defVal int
			edit   *walk.LineEdit
			target *int
		}

		fields := []*portField{
			{name: "Mixed", defVal: defaultMixed, target: &finalMixed},
			{name: "Socks", defVal: defaultSocks, target: &finalSocks},
			{name: "HTTP(S)", defVal: defaultHttp, target: &finalHttp},
		}

		children := make([]Widget, 0, len(fields)*2)
		for _, f := range fields {
			children = append(children,
				Label{Text: f.name + " 端口:", Alignment: AlignHFarVCenter},
				LineEdit{AssignTo: &f.edit, Text: strconv.Itoa(f.defVal)},
			)
		}

		var currentDlg *walk.Dialog

		res := RunEditor(e.activeOwner(), EditorConfig{
			Title:     "更改代理端口",
			Width:     320,
			MinHeight: 140,
			OnReady: func(dlg *walk.Dialog) {
				currentDlg = dlg
				e.dialogMgr.Register(key, dlg)
			},
			Widgets: []Widget{
				Composite{
					Layout:   Grid{Columns: 2, Spacing: 10, MarginsZero: true},
					Children: children,
				},
			},
			OnAccept: func() (bool, error) {
				parsed := make([]int, len(fields))
				for i, f := range fields {
					val, err := netutil.ParseAndValidatePort(f.edit.Text())
					if err != nil {
						RunErrorDialog(currentDlg, "输入错误", formatPortError(f.name, err))
						return false, nil
					}
					parsed[i] = val
				}

				for i, f := range fields {
					*f.target = parsed[i]
				}
				return true, nil
			},
		})

		isAccepted = res.Accepted
	})

	return finalMixed, finalSocks, finalHttp, isAccepted
}

func formatPortError(name string, err error) string {
	switch {
	case errors.Is(err, netutil.ErrEmptyPort):
		return name + " 端口不能为空，若禁用请填 0。"
	case errors.Is(err, netutil.ErrOutOfRange):
		return name + " 端口必须在 0 - 65535 之间。"
	case errors.Is(err, netutil.ErrInvalidType):
		return name + " 端口必须是纯数字。"
	default:
		return name + " 端口格式错误。"
	}
}
