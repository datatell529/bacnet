package server

import (
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/anviod/bacnet/btypes"
	"github.com/anviod/bacnet/encoding"
)

// blockingWriteHandler 阻塞指定时长并统计**峰值并发**。
type blockingWriteHandler struct {
	delay time.Duration
	cur   int32
	peak  int32
	total int32
}

func (h *blockingWriteHandler) WriteProperty(_ btypes.ObjectType, _ btypes.ObjectInstance, _ btypes.PropertyType, _ interface{}) error {
	cur := atomic.AddInt32(&h.cur, 1)
	for {
		peak := atomic.LoadInt32(&h.peak)
		if cur <= peak || atomic.CompareAndSwapInt32(&h.peak, peak, cur) {
			break
		}
	}
	atomic.AddInt32(&h.total, 1)
	time.Sleep(h.delay)
	atomic.AddInt32(&h.cur, -1)
	return nil
}

// 构造一条 WriteProperty(presentValue) 报文。
func buildWriteRequest(t *testing.T, objType btypes.ObjectType, instance btypes.ObjectInstance, value interface{}) []byte {
	t.Helper()
	enc := encoding.NewEncoder()
	wp := btypes.PropertyData{
		Object: btypes.Object{
			ID:         btypes.ObjectID{Type: objType, Instance: instance},
			Properties: []btypes.Property{{Type: btypes.PROP_PRESENT_VALUE, ArrayIndex: btypes.ArrayAll, Data: value}},
		},
	}
	if err := enc.WriteProperty(1, wp); err != nil {
		t.Fatalf("encode WriteProperty: %v", err)
	}
	full := enc.Bytes()
	var probe btypes.APDU
	if err := encoding.NewDecoder(full).APDU(&probe); err != nil {
		t.Fatalf("APDU 拆分失败: %v", err)
	}
	return buildBACnetMessage(t, full[:len(full)-len(probe.RawData)], probe.RawData, false)
}

// D-05：高密度请求下并发必须**有界**（不得超过 maxConcurrentRequests）。
func TestServe_BoundedConcurrency(t *testing.T) {
	h := &blockingWriteHandler{delay: 50 * time.Millisecond}
	srv, mockDL := newTestServerWithHandler(t, h)

	go func() { _ = srv.Serve() }()

	src := &btypes.Address{Adr: []byte{192, 168, 1, 100}, Len: 4}
	msg := buildWriteRequest(t, btypes.AnalogInput, 1, float64(1))
	const n = 400
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; i < n; i++ {
			mockDL.injectReceive(src, msg)
		}
	}()

	time.Sleep(300 * time.Millisecond)

	peak := atomic.LoadInt32(&h.peak)
	t.Logf("peak concurrent handlers=%d (cap=%d)", peak, maxConcurrentRequests)
	// 先确保注入协程结束（否则 Close 关闭 receiveCh 会与发送竞争）
	wg.Wait()
	srv.Close()

	if peak > maxConcurrentRequests {
		t.Fatalf("并发超上限：peak=%d cap=%d", peak, maxConcurrentRequests)
	}
	if peak < 2 {
		t.Fatalf("未观察到任何并发（peak=%d）——测试无效", peak)
	}
}

// D-05：接收缓冲必须归还池（此前 Serve 只 Get 不 Put）。
func TestServe_ReturnsBuffersToPool(t *testing.T) {
	srv, mockDL := newTestServer(t)

	var newCalls int32
	srv.readBufPool = sync.Pool{New: func() any {
		atomic.AddInt32(&newCalls, 1)
		return make([]byte, srv.config.MaxPDU)
	}}

	go func() { _ = srv.Serve() }()

	// 构造一条合法的 ReadProperty 请求
	enc := encoding.NewEncoder()
	rp := btypes.PropertyData{
		Object: btypes.Object{
			ID:         btypes.ObjectID{Type: btypes.AnalogInput, Instance: 1},
			Properties: []btypes.Property{{Type: btypes.PROP_PRESENT_VALUE, ArrayIndex: btypes.ArrayAll}},
		},
	}
	if err := enc.ReadProperty(1, rp); err != nil {
		t.Fatalf("encode ReadProperty: %v", err)
	}
	full := enc.Bytes()
	var probe btypes.APDU
	if err := encoding.NewDecoder(full).APDU(&probe); err != nil {
		t.Fatalf("APDU 拆分失败: %v", err)
	}
	msg := buildBACnetMessage(t, full[:len(full)-len(probe.RawData)], probe.RawData, false)

	src := &btypes.Address{Adr: []byte{192, 168, 1, 100}, Len: 4}
	// 逐个注入并等待应答：若缓冲被归还池，后续报文会复用同一块缓冲，
	// 分配次数应远小于报文数；旧实现（只 Get 不 Put）则每报文一次分配。
	const n = 30
	served := 0
	for i := 0; i < n; i++ {
		before := mockDL.sentCount()
		mockDL.injectReceive(src, msg)
		deadline := time.Now().Add(2 * time.Second)
		for mockDL.sentCount() == before && time.Now().Before(deadline) {
			time.Sleep(2 * time.Millisecond)
		}
		if mockDL.sentCount() == before {
			t.Fatalf("第 %d 个报文未得到应答", i)
		}
		served++
		time.Sleep(2 * time.Millisecond) // 等 worker 把缓冲归还池
	}
	srv.Close()

	got := atomic.LoadInt32(&newCalls)
	t.Logf("pool.New calls=%d for %d datagrams (served=%d)", got, n, served)
	if got >= n {
		t.Fatalf("缓冲未归还池：%d 个报文触发 %d 次分配（应当远小于）", n, got)
	}
}

// alwaysErrDataLink 让 Receive 立即返回错误，用于验证「错误不再紧循环空转」。
type alwaysErrDataLink struct {
	calls int64
}

func (d *alwaysErrDataLink) GetMyAddress() *btypes.Address {
	return &btypes.Address{Adr: []byte{127, 0, 0, 1}, Len: 4}
}
func (d *alwaysErrDataLink) GetBroadcastAddress() *btypes.Address {
	return &btypes.Address{MacLen: 0, Adr: []byte{255, 255, 255, 255}, Len: 4}
}
func (d *alwaysErrDataLink) Send([]byte, *btypes.NPDU, *btypes.Address) (int, error) { return 0, nil }
func (d *alwaysErrDataLink) Close() error                                            { return nil }
func (d *alwaysErrDataLink) Receive([]byte) (*btypes.Address, int, error) {
	atomic.AddInt64(&d.calls, 1)
	return nil, 0, errors.New("boom")
}

// D-05：接收持续出错时有界退避（旧实现是 `continue` 紧循环）。
func TestServe_RecvErrorBoundedBackoff(t *testing.T) {
	cfg := DefaultDeviceConfig()
	dl := &alwaysErrDataLink{}
	srv, err := NewServerWithDataLink(cfg, dl)
	if err != nil {
		t.Fatalf("NewServerWithDataLink: %v", err)
	}

	go func() { _ = srv.Serve() }()
	time.Sleep(300 * time.Millisecond)

	got := atomic.LoadInt64(&dl.calls)
	srv.Close()
	t.Logf("Receive calls in 300ms with persistent error = %d (backoff step=%s cap=%s)", got, recvBackoffStep, recvBackoffMax)

	if got == 0 {
		t.Fatal("未观察到任何 Receive 调用")
	}
	// 无退避的紧循环在 300ms 内会产生数十万次调用；有界退避应是个位/十位数。
	if got > 40 {
		t.Fatalf("接收错误仍在紧循环空转：300ms 内 %d 次 Receive", got)
	}
}
