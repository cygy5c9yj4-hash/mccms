package account

import (
	"fmt"
	"strings"
)

// 爱发电账号绑定。
//
// 绑定后订单中的 user_id 与本站账号一一对应，之后该爱发电账号的每一笔订单
// 都会自动归户，捐赠者不必再在备注或自定义信息里写用户名。
//
// 绑定来源分为两级：
//   1. 首次识别：订单的 custom_order_id（自定义信息）或 remark（备注）里出现本站用户名；
//   2. 永久自动：首次识别成功后写下 AfdianUserID，后续订单直接按该 ID 匹配。

// BindAfdian 把爱发电账号绑定到本站用户。
//
// 同一个爱发电账号只能绑定一个本站账号，绑定新账号时会先解绑旧账号，
// 避免同一笔捐赠被记到多个账号上。
func (s *Service) BindAfdian(userID, afdianUID string) error {
	afdianUID = strings.TrimSpace(afdianUID)
	if userID == "" || afdianUID == "" {
		return fmt.Errorf("%w: 参数不完整", ErrInvalid)
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	u, err := s.store.UserByID(userID)
	if err != nil {
		return err
	}
	if u == nil {
		return ErrNotFound
	}
	if u.AfdianUserID == afdianUID {
		return nil
	}

	// 先解除其他账号上对同一爱发电 ID 的占用。
	users, err := s.store.ListUsers()
	if err != nil {
		return err
	}
	for _, other := range users {
		if other.ID != u.ID && other.AfdianUserID == afdianUID {
			other.AfdianUserID = ""
			other.AfdianBoundAt = nil
			other.UpdatedAt = s.now()
			if err := s.store.UpdateUser(other); err != nil {
				return err
			}
		}
	}

	now := s.now()
	u.AfdianUserID = afdianUID
	u.AfdianBoundAt = &now
	u.UpdatedAt = now
	return s.store.UpdateUser(u)
}

// UnbindAfdian 解除当前账号的爱发电绑定。
//
// 解绑后该爱发电账号的新订单不再自动归户，需要重新认领；
// 已经发放的 VIP 权益不受影响。
func (s *Service) UnbindAfdian(userID string) error {
	if userID == "" {
		return fmt.Errorf("%w: 缺少用户", ErrInvalid)
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	u, err := s.store.UserByID(userID)
	if err != nil {
		return err
	}
	if u == nil {
		return ErrNotFound
	}
	if u.AfdianUserID == "" {
		return nil
	}
	u.AfdianUserID = ""
	u.AfdianBoundAt = nil
	u.UpdatedAt = s.now()
	return s.store.UpdateUser(u)
}

// UserByAfdianUID 返回绑定了该爱发电账号的本站用户；没有绑定时返回 nil。
func (s *Service) UserByAfdianUID(afdianUID string) *User {
	afdianUID = strings.TrimSpace(afdianUID)
	if afdianUID == "" {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	users, err := s.store.ListUsers()
	if err != nil {
		return nil
	}
	for _, u := range users {
		if u.AfdianUserID == afdianUID {
			return u
		}
	}
	return nil
}
