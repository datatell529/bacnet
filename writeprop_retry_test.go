package bacnet

import (
	"fmt"
	"net"
	"testing"
	"time"

	"github.com/anviod/bacnet/btypes"
	"github.com/anviod/bacnet/datalink"
)

// listenSilentUDP479xx binds a loopback UDP socket in the 479xx test band and
// drains everything it receives. It never answers, which makes it a stable
// "device is there but not responding" stub. Using a real listener (instead of
// an unused port) avoids ICMP port-unreachable side effects and keeps the test
// deterministic.
//
// listenSilentUDP479xx 在 479xx 端口段绑定本机回环 UDP socket，只收不回，
// 作为「设备存在但不响应」的稳定桩；相比使用无人监听的端口，可避免 ICMP
// 端口不可达带来的不确定性。
func listenSilentUDP479xx(t *testing.T) (net.PacketConn, int) {
	t.Helper()
	for port := 47950; port <= 47999; port++ {
		conn, err := net.ListenPacket("udp4", fmt.Sprintf("127.0.0.1:%d", port))
		if err != nil {
			continue
		}
		return conn, port
	}
	t.Fatal("no free UDP port in 47950-47999")
	return nil, 0
}

// freeUDPPort479xx returns a currently free loopback UDP port in the 479xx test
// band, excluding the port already taken by the silent stub.
func freeUDPPort479xx(t *testing.T, exclude int) int {
	t.Helper()
	for port := 47950; port <= 47999; port++ {
		if port == exclude {
			continue
		}
		conn, err := net.ListenPacket("udp4", fmt.Sprintf("127.0.0.1:%d", port))
		if err != nil {
			continue
		}
		conn.Close()
		return port
	}
	t.Fatal("no free UDP port in 47950-47999")
	return 0
}

// TestWritePropertyFailureSingleAttempt covers the P2 acceptance criterion:
// when the target never answers, a write must fail after ONE 5s receive
// timeout (5s~7s wall clock). The old "count < 2" loop waited twice (>=10s),
// so this test is red on the old implementation.
//
// TestWritePropertyFailureSingleAttempt 覆盖 P2 验收判据：目标设备不响应时，
// 一次写只应经历一次 5s 接收超时（实测 5s~7s）。旧实现 `count < 2` 会等两次
// （≥10s），因此该用例在旧代码上必然失败。
func TestWritePropertyFailureSingleAttempt(t *testing.T) {
	stub, stubPort := listenSilentUDP479xx(t)
	defer stub.Close()
	go func() {
		buf := make([]byte, 1500)
		for {
			if _, _, err := stub.ReadFrom(buf); err != nil {
				return
			}
		}
	}()

	clientPort := freeUDPPort479xx(t, stubPort)
	client, err := NewClient(&ClientBuilder{
		Ip:         "127.0.0.1",
		Port:       clientPort,
		SubnetCIDR: 8,
		MaxPDU:     btypes.MaxAPDU,
	})
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	defer client.Close()
	go client.ClientRun()

	dev := btypes.Device{
		DeviceID: 1001,
		Addr:     *datalink.IPPortToAddress(net.ParseIP("127.0.0.1"), stubPort),
		MaxApdu:  btypes.MaxAPDU,
		ID:       btypes.ObjectID{Type: btypes.DeviceType, Instance: 1001},
	}

	start := time.Now()
	err = client.WriteProperty(dev, btypes.PropertyData{
		Object: btypes.Object{
			ID: btypes.ObjectID{Type: btypes.AnalogValue, Instance: 1},
			Properties: []btypes.Property{{
				Type:       btypes.PROP_PRESENT_VALUE,
				ArrayIndex: btypes.ArrayAll,
				Data:       float32(1),
			}},
		},
	})
	elapsed := time.Since(start)

	if err == nil {
		t.Fatal("WriteProperty to a non-responding device returned nil error")
	}
	t.Logf("write failed after %v: %v", elapsed, err)

	const low, high = 5 * time.Second, 7 * time.Second
	if elapsed < low || elapsed > high {
		t.Fatalf("single failed write took %v, want [%v, %v] (old count<2 implementation would take >=10s)", elapsed, low, high)
	}
}
