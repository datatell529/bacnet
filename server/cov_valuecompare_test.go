package server

import (
	"math"
	"testing"

	"github.com/anviod/bacnet/btypes"
	"github.com/anviod/bacnet/encoding"
)

// =============================================================================
// Present_Value equality predicate — type normalization table
// =============================================================================

// TestCovValuesEqual_Table walks the normalization table documented in
// server/cov_valuecompare.go one row at a time.
//
// 中文说明：逐行验证相等判定的类型归一表。
func TestCovValuesEqual_Table(t *testing.T) {
	type tc struct {
		name string
		old  interface{}
		new  interface{}
		want bool
	}
	cases := []tc{
		// nil semantics
		{"nil_nil_equal", nil, nil, true},
		{"nil_to_value_changed", nil, float64(0), false},
		{"value_to_nil_changed", float64(0), nil, false},
		{"nil_to_string_changed", nil, "", false},

		// bool
		{"bool_true_true", true, true, true},
		{"bool_false_false", false, false, true},
		{"bool_true_false", true, false, false},
		// bool and numeric zero are different types -> changed (over-notify is safe)
		{"bool_false_vs_float_zero", false, float64(0), false},
		{"float_zero_vs_bool_false", float64(0), false, false},

		// numeric normalization (float64 comparison, no tolerance)
		{"int_vs_uint32", int(1), uint32(1), true},
		{"float32_vs_float64", float32(1.5), float64(1.5), true},
		{"float64_vs_float32", float64(1.5), float32(1.5), true},
		{"numeric_different", float64(1.5), float64(1.6), false},
		{"uint16_vs_int8", uint16(7), int8(7), true},
		{"enumerated_vs_enumerated", btypes.Enumerated(1), btypes.Enumerated(1), true},
		{"enumerated_different", btypes.Enumerated(1), btypes.Enumerated(2), false},
		{"enumerated_vs_uint32", btypes.Enumerated(1), uint32(1), true},
		{"no_tolerance", float64(1.0), float64(1.0 + 1e-9), false},
		// documented precision boundary: float32 -> float64 promotion is lossy
		{"float64_vs_float32_lossy", float64(0.1), float32(0.1), false},
		// IEEE NaN never equals anything -> keeps notifying (documented limitation)
		{"nan_vs_nan", math.NaN(), math.NaN(), false},

		// string
		{"string_equal", "abc", "abc", true},
		{"string_different", "abc", "abd", false},
		{"string_vs_number", "1", 1, false},

		// fallback: reflect.DeepEqual, never ==
		{
			"struct_deep_equal",
			btypes.ObjectID{Type: btypes.AnalogInput, Instance: 1},
			btypes.ObjectID{Type: btypes.AnalogInput, Instance: 1},
			true,
		},
		{
			"struct_different",
			btypes.ObjectID{Type: btypes.AnalogInput, Instance: 1},
			btypes.ObjectID{Type: btypes.AnalogInput, Instance: 2},
			false,
		},
		{"bitstring_deep_equal", btypes.NewBitString(4), btypes.NewBitString(4), true},
		{"slice_deep_equal", []byte{1, 2}, []byte{1, 2}, true},
		{"slice_different", []byte{1, 2}, []byte{1, 3}, false},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := covValuesEqual(c.old, c.new); got != c.want {
				t.Errorf("covValuesEqual(%#v, %#v) = %v, want %v", c.old, c.new, got, c.want)
			}
			// Symmetry: the predicate must not depend on argument order.
			if got := covValuesEqual(c.new, c.old); got != c.want {
				t.Errorf("covValuesEqual(%#v, %#v) [reversed] = %v, want %v", c.new, c.old, got, c.want)
			}
		})
	}
}

// =============================================================================
// COV notification observation helpers (no sockets, no sleeps)
// =============================================================================

// covTestSubAddr is the subscriber address recorded for the fake subscription.
func covTestSubAddr() btypes.Address {
	mac := []byte{127, 0, 0, 1, 0xBA, 0xC0} // IPv4 + port, BACnet/IP shape
	return btypes.Address{
		Net:    0,
		MacLen: 6,
		Mac:    mac,
		Adr:    mac,
		Len:    6,
	}
}

// newCOVTestServer builds a server on the recording mock data link. Nothing is
// bound to a socket and Serve() is never started, so the cases below are purely
// synchronous and clock independent.
//
// 中文说明：用记录型 mock datalink 建服务端，不绑端口、不起 Serve，用例完全同步。
func newCOVTestServer(t *testing.T) (*server, *mockDataLink) {
	t.Helper()
	cfg := DefaultDeviceConfig()
	cfg.DeviceID = 1000
	cfg.DeviceName = "COVTestServer"
	cfg.VendorID = 999

	dl := newMockDataLink()
	srv, err := NewServerWithDataLink(cfg, dl)
	if err != nil {
		t.Fatalf("NewServerWithDataLink: %v", err)
	}
	return srv.(*server), dl
}

// addCOVTestObject registers an object whose Present_Value is already stored as
// initialPV (so the first SetProperty is a value comparison, not a first write).
// Extra properties are merged in; pass a non-nil list to omit Present_Value.
//
// 中文说明：注册带初始 PV 的测试对象。
func addCOVTestObject(t *testing.T, s *server, objType btypes.ObjectType, instance btypes.ObjectInstance, initialPV interface{}) {
	t.Helper()
	if err := s.AddObject(btypes.Object{
		ID: btypes.ObjectID{Type: objType, Instance: instance},
		Properties: []btypes.Property{
			{Type: btypes.PROP_PRESENT_VALUE, ArrayIndex: btypes.ArrayAll, Data: initialPV},
		},
	}); err != nil {
		t.Fatalf("AddObject(%v,%d): %v", objType, instance, err)
	}
}

// subscribeCOVForTest installs a permanent COV subscription for the object
// directly in the manager, isolating the SetProperty behaviour under test from
// the initial notification that handleSubscribeCOV sends on acceptance.
//
// 中文说明：直接向 covManager 登记永久订阅，避免订阅受理时的初始通知干扰计数。
func subscribeCOVForTest(s *server, objType btypes.ObjectType, instance btypes.ObjectInstance) {
	addr := covTestSubAddr()
	s.cov.subscribe(&covSubscription{
		processID:      1,
		objectType:     objType,
		objectInstance: instance,
		permanent:      true,
		subscriberAddr: addr,
	})
}

// covDatabaseRevision reads the device object's database-revision counter.
//
// 中文说明：读取 Device 对象的 database-revision 计数。
func covDatabaseRevision(t *testing.T, s *server) uint32 {
	t.Helper()
	v, found := s.GetProperty(btypes.DeviceType, s.GetDeviceID(), btypes.PROP_DATABASE_REVISION)
	if !found {
		t.Fatal("database revision property not found")
	}
	rev, ok := v.(uint32)
	if !ok {
		t.Fatalf("database revision type = %T, want uint32", v)
	}
	return rev
}

// countCOVNotifications decodes every captured frame exactly the way a client
// would (BVLC -> NPDU -> APDU) and counts COV notifications, confirmed or
// unconfirmed. This is the "notification counter" the acceptance items talk
// about: it observes the wire, not an internal hook.
//
// 中文说明：把 mock datalink 捕获的每帧按 BVLC→NPDU→APDU 回解，统计 COV 通知条数
// （确认/非确认都算）。这是"通知次数"的观测口径——看线上报文，而非内部计数。
func countCOVNotifications(t *testing.T, dl *mockDataLink) int {
	t.Helper()
	count := 0
	for i, frame := range dl.sentData {
		var bvlc btypes.BVLC
		if err := encoding.NewDecoder(frame).BVLC(&bvlc); err != nil {
			t.Fatalf("frame %d: BVLC decode: %v", i, err)
		}
		if len(frame) <= mtuHeaderLength {
			t.Fatalf("frame %d: shorter than BVLC header: %d bytes", i, len(frame))
		}
		dec := encoding.NewDecoder(frame[mtuHeaderLength:])
		var npdu btypes.NPDU
		if _, err := dec.NPDU(&npdu); err != nil {
			t.Fatalf("frame %d: NPDU decode: %v", i, err)
		}
		var apdu btypes.APDU
		if err := dec.APDU(&apdu); err != nil {
			t.Fatalf("frame %d: APDU decode: %v", i, err)
		}
		switch apdu.DataType {
		case btypes.UnconfirmedServiceRequest:
			if apdu.UnconfirmedService == btypes.ServiceUnconfirmedCOVNotification {
				count++
			}
		case btypes.ConfirmedServiceRequest:
			if apdu.Service == btypes.ServiceConfirmedCOVNotification {
				count++
			}
		}
	}
	return count
}

// =============================================================================
// Acceptance items 1-4: equal value => no notification, changed value => notify
// =============================================================================

// TestCOV_SetPropertyValueComparison covers acceptance items 1, 2 and 4 for
// bool / analog float64 / analog float32 / string Present_Values:
//
//   - item 1: two consecutive writes of the same value produce exactly ONE
//     notification when the value had to be created (first write), and ZERO
//     when the store already held that value;
//   - item 2: every write that changes the value notifies;
//   - item 4: the four type families each get their own case.
func TestCOV_SetPropertyValueComparison(t *testing.T) {
	type family struct {
		name     string
		objType  btypes.ObjectType
		initial  interface{} // value already in the store
		same     interface{} // numerically/bytically equal to initial
		next     interface{} // first different value
		nextSame interface{} // equal to next, expressed in another numeric type when useful
		last     interface{} // second different value
	}

	families := []family{
		{
			name:     "Binary_bool",
			objType:  btypes.BinaryInput,
			initial:  false,
			same:     false,
			next:     true,
			nextSame: true,
			last:     false,
		},
		{
			name:     "Analog_float64",
			objType:  btypes.AnalogInput,
			initial:  float64(23.5),
			same:     float64(23.5),
			next:     float64(24.5),
			nextSame: float64(24.5),
			last:     float64(25.5),
		},
		{
			name:     "Analog_float32_cross_type",
			objType:  btypes.AnalogValue,
			initial:  float32(1.5),
			same:     float32(1.5),
			next:     float64(2.5),
			nextSame: float32(2.5), // float32 vs float64 2.5: same value after normalization
			last:     float32(3.5),
		},
		{
			name:     "CharacterString",
			objType:  btypes.CharacterString,
			initial:  "supply-air",
			same:     "supply-air",
			next:     "return-air",
			nextSame: "return-air",
			last:     "exhaust-air",
		},
	}

	for _, f := range families {
		f := f
		t.Run(f.name, func(t *testing.T) {
			t.Run("stored_value_equal_writes_do_not_notify", func(t *testing.T) {
				s, dl := newCOVTestServer(t)
				addCOVTestObject(t, s, f.objType, 1, f.initial)
				subscribeCOVForTest(s, f.objType, 1)

				// Equal to what the store already holds: no change, no notification.
				for i := 0; i < 2; i++ {
					if err := s.SetProperty(f.objType, 1, btypes.PROP_PRESENT_VALUE, f.same); err != nil {
						t.Fatalf("SetProperty(same) #%d: %v", i+1, err)
					}
				}
				if got := countCOVNotifications(t, dl); got != 0 {
					t.Fatalf("equal Present_Value writes sent %d COV notifications, want 0", got)
				}

				// Item 2: real changes always notify, once per change.
				if err := s.SetProperty(f.objType, 1, btypes.PROP_PRESENT_VALUE, f.next); err != nil {
					t.Fatalf("SetProperty(next): %v", err)
				}
				if got := countCOVNotifications(t, dl); got != 1 {
					t.Fatalf("first changed write: %d COV notifications, want 1", got)
				}

				// Re-writing the new value is not a change (cross-type equal value included).
				if err := s.SetProperty(f.objType, 1, btypes.PROP_PRESENT_VALUE, f.nextSame); err != nil {
					t.Fatalf("SetProperty(nextSame): %v", err)
				}
				if got := countCOVNotifications(t, dl); got != 1 {
					t.Fatalf("re-writing the same value sent a notification, want still 1 (got %d)", got)
				}

				if err := s.SetProperty(f.objType, 1, btypes.PROP_PRESENT_VALUE, f.last); err != nil {
					t.Fatalf("SetProperty(last): %v", err)
				}
				if got := countCOVNotifications(t, dl); got != 2 {
					t.Fatalf("second changed write: %d COV notifications, want 2", got)
				}
			})

			t.Run("first_write_notifies_then_equal_write_does_not", func(t *testing.T) {
				s, dl := newCOVTestServer(t)
				// Object without a stored Present_Value: the write creates it.
				if err := s.AddObject(btypes.Object{
					ID: btypes.ObjectID{Type: f.objType, Instance: 2},
					Properties: []btypes.Property{
						{Type: btypes.PROP_OBJECT_NAME, ArrayIndex: btypes.ArrayAll, Data: "no-pv-yet"},
					},
				}); err != nil {
					t.Fatalf("AddObject: %v", err)
				}
				if _, found := s.GetProperty(f.objType, 2, btypes.PROP_PRESENT_VALUE); found {
					t.Fatal("test setup: Present_Value should not exist before the first write")
				}
				subscribeCOVForTest(s, f.objType, 2)

				// Item 3: creating the property is a change.
				if err := s.SetProperty(f.objType, 2, btypes.PROP_PRESENT_VALUE, f.next); err != nil {
					t.Fatalf("first SetProperty: %v", err)
				}
				if got := countCOVNotifications(t, dl); got != 1 {
					t.Fatalf("first write: %d COV notifications, want 1", got)
				}

				// Item 1: the immediate repeat is equal -> no second notification.
				if err := s.SetProperty(f.objType, 2, btypes.PROP_PRESENT_VALUE, f.next); err != nil {
					t.Fatalf("second SetProperty: %v", err)
				}
				if got := countCOVNotifications(t, dl); got != 1 {
					t.Fatalf("repeated equal write: %d COV notifications, want 1", got)
				}
			})
		})
	}
}

// TestCOV_FirstWriteWithoutStoredPresentValueNotifies pins acceptance item 3 on
// its own: no stored value => treated as changed => exactly one notification.
func TestCOV_FirstWriteWithoutStoredPresentValueNotifies(t *testing.T) {
	s, dl := newCOVTestServer(t)
	if err := s.AddObject(btypes.Object{
		ID: btypes.ObjectID{Type: btypes.AnalogInput, Instance: 7},
		Properties: []btypes.Property{
			{Type: btypes.PROP_OBJECT_NAME, ArrayIndex: btypes.ArrayAll, Data: "ai-7"},
		},
	}); err != nil {
		t.Fatalf("AddObject: %v", err)
	}
	subscribeCOVForTest(s, btypes.AnalogInput, 7)

	if err := s.SetProperty(btypes.AnalogInput, 7, btypes.PROP_PRESENT_VALUE, float64(42)); err != nil {
		t.Fatalf("SetProperty: %v", err)
	}
	if got := countCOVNotifications(t, dl); got != 1 {
		t.Fatalf("first write emitted %d COV notifications, want 1", got)
	}
	if v, found := s.GetProperty(btypes.AnalogInput, 7, btypes.PROP_PRESENT_VALUE); !found || v != float64(42) {
		t.Fatalf("store PV = %#v (found=%v), want 42", v, found)
	}
}

// =============================================================================
// Acceptance item 5: subscription acceptance still sends exactly one notification
// =============================================================================

// TestCOV_SubscribeDeliversExactlyOneInitialNotification drives the real
// handleSubscribeCOV path (regression guard for the initial notification) and
// then checks that a same-value SetProperty adds nothing on top of it.
func TestCOV_SubscribeDeliversExactlyOneInitialNotification(t *testing.T) {
	s, dl := newCOVTestServer(t)
	addCOVTestObject(t, s, btypes.AnalogValue, 1, float64(12.5))

	// Build a real SubscribeCOV request APDU and hand it to the handler.
	const processID = uint32(7)
	reqEnc := encoding.NewEncoder()
	if err := reqEnc.SubscribeCOV(1, btypes.SubscribeCOVData{
		ProcessID:                   processID,
		ObjectID:                    btypes.ObjectID{Type: btypes.AnalogValue, Instance: 1},
		IssueConfirmedNotifications: false,
		Lifetime:                    60,
	}); err != nil {
		t.Fatalf("encode SubscribeCOV: %v", err)
	}
	var reqAPDU btypes.APDU
	if err := encoding.NewDecoder(reqEnc.Bytes()).APDU(&reqAPDU); err != nil {
		t.Fatalf("decode SubscribeCOV APDU: %v", err)
	}

	src := covTestSubAddr()
	s.handleSubscribeCOV(&src, &btypes.NPDU{Priority: btypes.Normal}, &reqAPDU)

	if got := s.cov.count(); got != 1 {
		t.Fatalf("active subscriptions = %d, want 1", got)
	}
	if got := countCOVNotifications(t, dl); got != 1 {
		t.Fatalf("after subscription: %d COV notifications, want exactly 1", got)
	}

	// Same value as the store holds => still only the initial notification.
	if err := s.SetProperty(btypes.AnalogValue, 1, btypes.PROP_PRESENT_VALUE, float64(12.5)); err != nil {
		t.Fatalf("SetProperty(same): %v", err)
	}
	if got := countCOVNotifications(t, dl); got != 1 {
		t.Fatalf("same-value write after subscribe: %d COV notifications, want 1", got)
	}

	// A real change is still delivered.
	if err := s.SetProperty(btypes.AnalogValue, 1, btypes.PROP_PRESENT_VALUE, float64(13.5)); err != nil {
		t.Fatalf("SetProperty(changed): %v", err)
	}
	if got := countCOVNotifications(t, dl); got != 2 {
		t.Fatalf("changed write after subscribe: %d COV notifications, want 2", got)
	}
}

// =============================================================================
// Acceptance item 6: non-Present_Value writes never notify
// =============================================================================

// TestCOV_NonPresentValueWritesNeverNotify asserts that status_flags,
// reliability (and out_of_service) SetProperty calls produce no COV traffic at
// all, even with an active subscription on the same object.
func TestCOV_NonPresentValueWritesNeverNotify(t *testing.T) {
	s, dl := newCOVTestServer(t)
	if err := s.AddObject(btypes.Object{
		ID: btypes.ObjectID{Type: btypes.AnalogInput, Instance: 3},
		Properties: []btypes.Property{
			{Type: btypes.PROP_PRESENT_VALUE, ArrayIndex: btypes.ArrayAll, Data: float64(5.0)},
			{Type: btypes.PROP_STATUS_FLAGS, ArrayIndex: btypes.ArrayAll, Data: btypes.NewBitString(4)},
			{Type: btypes.PROP_RELIABILITY, ArrayIndex: btypes.ArrayAll, Data: uint32(0)},
			{Type: btypes.PROP_OUT_OF_SERVICE, ArrayIndex: btypes.ArrayAll, Data: false},
		},
	}); err != nil {
		t.Fatalf("AddObject: %v", err)
	}
	subscribeCOVForTest(s, btypes.AnalogInput, 3)

	writes := []struct {
		prop btypes.PropertyType
		data interface{}
	}{
		{btypes.PROP_STATUS_FLAGS, btypes.NewBitString(4)},
		{btypes.PROP_STATUS_FLAGS, btypes.NewBitString(4)},
		{btypes.PROP_RELIABILITY, uint32(1)},
		{btypes.PROP_RELIABILITY, uint32(1)},
		{btypes.PROP_OUT_OF_SERVICE, true},
	}
	for i, w := range writes {
		if err := s.SetProperty(btypes.AnalogInput, 3, w.prop, w.data); err != nil {
			t.Fatalf("SetProperty #%d (%v): %v", i+1, w.prop, err)
		}
	}

	if got := countCOVNotifications(t, dl); got != 0 {
		t.Fatalf("non-Present_Value writes produced %d COV notifications, want 0", got)
	}
	if got := dl.sentCount(); got != 0 {
		t.Fatalf("non-Present_Value writes sent %d packets, want 0", got)
	}

	// Sanity: the same object still notifies on a real Present_Value change.
	if err := s.SetProperty(btypes.AnalogInput, 3, btypes.PROP_PRESENT_VALUE, float64(6.0)); err != nil {
		t.Fatalf("SetProperty(PV): %v", err)
	}
	if got := countCOVNotifications(t, dl); got != 1 {
		t.Fatalf("changed Present_Value: %d COV notifications, want 1", got)
	}
}

// =============================================================================
// Store semantics: an "equal" value is still written
// =============================================================================

// TestCOV_EqualValueStillWritesStore proves the notification gate never
// short-circuits the store write: writing a numerically equal value of a
// different Go type stores that value (observable through GetProperty) while
// emitting no notification. It also pins the unchanged error contract for an
// unknown object.
func TestCOV_EqualValueStillWritesStore(t *testing.T) {
	s, dl := newCOVTestServer(t)
	addCOVTestObject(t, s, btypes.AnalogInput, 4, float64(1.0))
	subscribeCOVForTest(s, btypes.AnalogInput, 4)

	// int(1) == float64(1) under the numeric rule => no notification ...
	if err := s.SetProperty(btypes.AnalogInput, 4, btypes.PROP_PRESENT_VALUE, int(1)); err != nil {
		t.Fatalf("SetProperty(int 1): %v", err)
	}
	if got := countCOVNotifications(t, dl); got != 0 {
		t.Fatalf("equal-value write sent %d COV notifications, want 0", got)
	}
	// ... but the store must hold the newly written value (write not skipped).
	got, found := s.GetProperty(btypes.AnalogInput, 4, btypes.PROP_PRESENT_VALUE)
	if !found {
		t.Fatal("Present_Value missing after write")
	}
	if v, ok := got.(int); !ok || v != 1 {
		t.Fatalf("stored PV = %#v (%T), want int(1) — the write was skipped", got, got)
	}

	// Version/revision semantics are untouched on equal writes as well: the store
	// still counts the write, exactly as before this batch.
	revBefore := covDatabaseRevision(t, s)
	if err := s.SetProperty(btypes.AnalogInput, 4, btypes.PROP_PRESENT_VALUE, int(1)); err != nil {
		t.Fatalf("SetProperty repeat: %v", err)
	}
	if revAfter := covDatabaseRevision(t, s); revAfter != revBefore+1 {
		t.Fatalf("database revision after equal write = %d, want %d", revAfter, revBefore+1)
	}

	// Unknown object: error contract unchanged, no notification.
	if err := s.SetProperty(btypes.AnalogInput, 99, btypes.PROP_PRESENT_VALUE, float64(1)); err == nil {
		t.Fatal("SetProperty on unknown object returned nil error")
	}
	if got := countCOVNotifications(t, dl); got != 0 {
		t.Fatalf("failed write sent %d COV notifications, want 0", got)
	}
}
