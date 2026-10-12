package ui

import (
	"github.com/tailscale/walk"
)


func SetupDialogKeyFlow(dlg *walk.Dialog, acceptPB, cancelPB *walk.PushButton) func() {
	
	// 返回一个无副作用的空函数，防止外部 defer 调用崩溃
	return func() {}
}
