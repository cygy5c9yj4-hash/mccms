// Package mc 是 mccms 的核心：实体、配置、文本工具与错误类型。
package mc

import "fmt"

// McError 是所有业务错误的基类。
type McError struct {
	Msg     string
	Context map[string]any
	Kind    string
}

func (e *McError) Error() string { return e.Msg }

func NewError(msg string, ctx map[string]any) *McError {
	return &McError{Msg: msg, Context: ctx, Kind: "error"}
}

func Errorf(format string, args ...any) *McError {
	return NewError(fmt.Sprintf(format, args...), nil)
}

// ---- 访问权限类错误 ----------------------------------------------------------
//
// 站点拒绝提供内容时统一映射到这几个类型，调用方据此区分
// 「需要登录」「需要会员」「其他不可访问」，并给出可操作提示。
// 本库不做任何绕过访问控制的尝试。

// AccessDeniedError 是所有「无权访问」错误的基类。
type AccessDeniedError struct {
	McError
	RawMsg     string
	AccessCode int
	AccessType string
}

func (e *AccessDeniedError) Unwrap() error { return &e.McError }

// LoginRequiredError 需要登录态。
type LoginRequiredError struct{ AccessDeniedError }

// VipRequiredError 需要会员 / 金币等权益。
type VipRequiredError struct{ AccessDeniedError }

// ChapterNotAccessibleError 章节当前不可访问（下架 / 未发布等）。
type ChapterNotAccessibleError struct{ AccessDeniedError }

// RaiseAccessError 把站点返回的拒绝原因归一化成对应的错误类型。
func RaiseAccessError(msg string, ctx map[string]any, accessCode int, accessType string) error {
	if ctx == nil {
		ctx = map[string]any{}
	}
	ctx["raw_msg"] = msg
	ctx["access_code"] = accessCode
	if accessType != "" {
		ctx["access_type"] = accessType
	}

	base := AccessDeniedError{
		McError:    McError{Context: ctx, Kind: "access_denied"},
		RawMsg:     msg,
		AccessCode: accessCode,
		AccessType: accessType,
	}

	switch {
	case accessCode == 2 || accessType == "login":
		base.Msg = msg + " —— 该内容需要登录后才能访问。请配置你自己的账号（client.cookies 或 client.username/password）"
		return &LoginRequiredError{base}
	case accessType == "vip" || accessType == "cion" || accessType == "ticket" || accessType == "pay":
		base.Msg = msg + " —— 该内容需要会员/金币权益，当前账号无权访问"
		return &VipRequiredError{base}
	default:
		base.Msg = msg + " —— 该章节当前不可访问（可能是权益、下架或未发布）"
		return &ChapterNotAccessibleError{base}
	}
}

// IsAccessDenied 判断是否为权限类错误。
func IsAccessDenied(err error) bool {
	switch err.(type) {
	case *AccessDeniedError, *LoginRequiredError, *VipRequiredError, *ChapterNotAccessibleError:
		return true
	}
	return false
}

// ErrorMessage 取错误的可读信息。
func ErrorMessage(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}
