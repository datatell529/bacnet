package server

import "reflect"

// This file holds the Present_Value equality predicate used by the COV
// notification path. BACnet COV semantics say a notification is due when the
// monitored value changes, not on every southbound sampling tick, so
// server.SetProperty compares the incoming value with the stored one before
// deciding whether to notify. The store is written either way — only the
// notification is gated.
//
// 本文件只放"Present_Value 是否真的变了"的相等判定。BACnet COV 语义要求值变化
// 才通知；南向每个采样周期都写一次同值不应刷屏。store 照常写入，只对通知做门控。

// covValuesEqual reports whether newValue is considered the same value as the
// value currently stored (oldValue). It is deliberately conservative: whenever
// the two values cannot be shown to be equal under the rules below it returns
// false, i.e. an unnecessary notification is preferred over a missed one.
//
// Rules (no "roughly equal" anywhere):
//   - nil vs nil        -> equal
//   - nil vs non-nil    -> changed
//   - bool              -> equal only if both are bools with the same value
//   - numbers           -> all integer kinds (including named types such as
//     btypes.Enumerated) and float32/float64 are normalized to float64 and
//     compared with ==. No tolerance / cov-increment filtering here.
//   - string            -> byte-for-byte equal
//   - anything else     -> reflect.DeepEqual (never ==, which panics for
//     uncomparable types such as structs/slices)
//
// 中文说明：相等判定。nil↔nil 相等、nil↔非 nil 变化；布尔同型同值相等；所有整型
// （含 btypes.Enumerated 这类命名类型）与 float32/float64 一律归一到 float64 用 ==
// 比较（不做容差，cov-increment 属下一版）；字符串逐字节比较；其余类型用
// reflect.DeepEqual 兜底（绝不用 ==，结构体/切片会 panic）。类型不同即视为变化。
func covValuesEqual(oldValue, newValue interface{}) bool {
	if oldValue == nil || newValue == nil {
		// nil <-> nil means "no value before, no value now": nothing changed.
		// 中文说明：nil ↔ nil 视为相等（不通知）；一侧为 nil 另一侧不是则视为变化。
		return oldValue == nil && newValue == nil
	}

	switch n := newValue.(type) {
	case bool:
		o, ok := oldValue.(bool)
		return ok && o == n
	case string:
		o, ok := oldValue.(string)
		return ok && o == n
	}

	oldKind := reflect.TypeOf(oldValue).Kind()
	newKind := reflect.TypeOf(newValue).Kind()
	if covNumericKind(oldKind) && covNumericKind(newKind) {
		// Covers btypes.Enumerated (uint32), uint32/uint16/... , int/... and
		// float32/float64 on both sides — a float32 PV written over a float64
		// store entry (or the reverse) still compares by value.
		return covNumericToFloat64(oldValue, oldKind) == covNumericToFloat64(newValue, newKind)
	}

	return reflect.DeepEqual(oldValue, newValue)
}

// covNumericKind reports whether k is an integer or floating point kind.
//
// 中文说明：判断反射 Kind 是否属于整型/浮点型（命名类型如 btypes.Enumerated
// 的 Kind 仍是 Uint32，因此一并覆盖）。
func covNumericKind(k reflect.Kind) bool {
	switch k {
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64,
		reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64, reflect.Uintptr,
		reflect.Float32, reflect.Float64:
		return true
	default:
		return false
	}
}

// covNumericToFloat64 normalizes any numeric value to float64. Callers must have
// checked covNumericKind first.
//
// 中文说明：把任意数值归一到 float64；调用方需先用 covNumericKind 判定。
func covNumericToFloat64(v interface{}, k reflect.Kind) float64 {
	rv := reflect.ValueOf(v)
	switch k {
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		return float64(rv.Int())
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64, reflect.Uintptr:
		return float64(rv.Uint())
	default: // reflect.Float32, reflect.Float64
		return rv.Float()
	}
}
