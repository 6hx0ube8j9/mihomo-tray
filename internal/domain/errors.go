package domain

import "errors"

var (
	// 运行时与内核状态
	ErrKernelNotReady = errors.New("内核尚未就绪")
	ErrSystemBusy     = errors.New("系统繁忙，请稍候重试")
	ErrTunDeviceBusy  = errors.New("虚拟网卡已被其他程序占用")

	// 代理与网络配置
	ErrNoProxyPort = errors.New("未检测到有效代理端口")

	// 用户配置与输入
	ErrInvalidPort = errors.New("端口超出合法范围 (0-65535)")
)

type UserError struct {
	Msg string
	Err error
}

func (e *UserError) Error() string {
	if e.Err == nil {
		return e.Msg
	}
	return e.Msg + ": " + e.Err.Error()
}

func (e *UserError) Unwrap() error {
	return e.Err
}

func WrapUserError(msg string, err error) error {
	if err == nil {
		return nil
	}
	return &UserError{Msg: msg, Err: err}
}

func UserMessage(err error) string {
	if err == nil {
		return ""
	}
	var ue *UserError
	if errors.As(err, &ue) {
		return ue.Msg
	}
	return err.Error()
}
