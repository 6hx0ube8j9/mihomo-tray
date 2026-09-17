package ui

import (
	"github.com/tailscale/walk"
	. "github.com/tailscale/walk/declarative"
)

var isSubscriptionEditorOpen bool

func (e *UIEngine) ShowSubscriptionEditor(title, defaultName, defaultUrl string, defaultInterval int) (string, string, int, bool) {
	if e.app == nil || e.mw == nil {
		return "", "", 0, false
	}

	type result struct {
		name, url string
		interval  int
		ok        bool
	}
	resCh := make(chan result)

	e.app.Synchronize(func() {
		if isSubscriptionEditorOpen {
			resCh <- result{ok: false}
			return
		}
		isSubscriptionEditorOpen = true

		var dlg *walk.Dialog
		var nameEdit *walk.LineEdit
		var urlEdit *walk.LineEdit
		var intervalEdit *walk.NumberEdit
		var acceptButton *walk.PushButton

		var outName, outUrl string
		var outInterval int
		var accepted bool

		var owner walk.Form = e.mw
		if e.panelWindow != nil && e.panelWindow.Visible() {
			owner = e.panelWindow
		}

		err := Dialog{
			AssignTo: &dlg,
			Title:    title,
			MinSize:  Size{Width: 420, Height: 200},
			Layout:   VBox{},
			Children: []Widget{
				Composite{
					Layout: Grid{Columns: 2},
					Children: []Widget{
						Label{Text: "配置名称:"},
						LineEdit{AssignTo: &nameEdit, Text: defaultName},

						Label{Text: "订阅链接:"},
						LineEdit{AssignTo: &urlEdit, Text: defaultUrl},

						Label{Text: "更新间隔(天):"},
						NumberEdit{AssignTo: &intervalEdit, Value: float64(defaultInterval), MinValue: 0, MaxValue: 30},
					},
				},
				Composite{
					Layout: HBox{},
					Children: []Widget{
						HSpacer{},
						PushButton{
							AssignTo: &acceptButton,
							Text:     "确定",
							OnClicked: func() {
								outName = nameEdit.Text()
								outUrl = urlEdit.Text()
								outInterval = int(intervalEdit.Value())
								accepted = true
								dlg.Accept()
							},
						},
						PushButton{
							Text: "取消",
							OnClicked: func() {
								dlg.Cancel()
							},
						},
					},
				},
			},
		}.Create(owner)

		if err != nil {
			isSubscriptionEditorOpen = false
			resCh <- result{ok: false}
			return
		}

		dlg.Closing().Attach(func(canceled *bool, reason walk.CloseReason) {
			isSubscriptionEditorOpen = false
		})

		dlg.Run()
		resCh <- result{outName, outUrl, outInterval, accepted}
	})

	res := <-resCh
	return res.name, res.url, res.interval, res.ok
}
