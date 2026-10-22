// Copyright 2024 The BACnet Authors. All rights reserved.
// Use of this source code is governed by a MIT license
// that can be found in the LICENSE file.

package server

import "github.com/anviod/bacnet/btypes"

// expandObjectProps expands the RPM pseudo-properties ALL / REQUIRED / OPTIONAL
// into a concrete property list for any object type.
//
// Device objects keep using ListDeviceProperties (device-level properties live
// outside the regular object map). Every other object type returns the full set
// of properties stored for that object, with Present_Value normalized exactly
// like a normal single-property read. Browsers such as YABE rely on this to
// show real object names instead of "AI 0"/"AI 1" placeholders.
//
// 中文说明：expandObjectProps 将 RPM 的 ALL/REQUIRED/OPTIONAL 伪属性展开为对象
// 的具体属性列表。Device 对象继续使用 ListDeviceProperties；其他对象类型返回
// 对象存储中的全部属性，Present_Value 按既有规则做规范化。YABE 等浏览器依赖此
// 行为显示真实的 Object_Name，而不是 "AI 0"/"AI 1" 之类的占位名。
func (s *server) expandObjectProps(objType btypes.ObjectType, objInstance btypes.ObjectInstance) []btypes.Property {
	if objType == btypes.DeviceType {
		if !s.store.DevicePropertyExists(objInstance) {
			return nil
		}
		return s.store.ListDeviceProperties()
	}

	obj, ok := s.store.GetObject(objType, objInstance)
	if !ok {
		return nil
	}

	props := make([]btypes.Property, 0, len(obj.Properties))
	for _, pr := range obj.Properties {
		data := pr.Data
		if pr.Type == btypes.PROP_PRESENT_VALUE {
			data = normalizePresentValue(objType, data)
		}
		props = append(props, btypes.Property{
			Type:       pr.Type,
			ArrayIndex: pr.ArrayIndex,
			Data:       data,
		})
	}
	return props
}
