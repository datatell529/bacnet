package datalink

import (
	"context"
	"fmt"
	//"log"
	"net"
	"strings"
	"time"

	"github.com/anviod/bacnet/btypes"
)

// GetLocalIP returns a non-loopback IPv4 address of the local machine.
// Returns "0.0.0.0" if no suitable address is found.
func GetLocalIP() net.IP {
	ifaces, err := net.Interfaces()
	if err != nil {
		return net.IPv4(0, 0, 0, 0)
	}
	var fallback net.IP
	for _, iface := range ifaces {
		if iface.Flags&net.FlagUp == 0 || iface.Flags&net.FlagLoopback != 0 {
			continue
		}
		addrs, err := iface.Addrs()
		if err != nil {
			continue
		}
		for _, addr := range addrs {
			var ip net.IP
			var ones int
			switch v := addr.(type) {
			case *net.IPNet:
				ip = v.IP
				ones, _ = v.Mask.Size()
			case *net.IPAddr:
				ip = v.IP
			}
			ip = ip.To4()
			if ip == nil || ip.IsLoopback() || ip.IsLinkLocalUnicast() {
				continue
			}
			if fallback == nil {
				fallback = ip
			}
			if ones > 0 && ones < 32 {
				return ip
			}
		}
	}
	if fallback != nil {
		return fallback
	}
	return net.IPv4(0, 0, 0, 0)
}

// DefaultPort that BacnetIP will use if a port is not given. Valid ports for
// the bacnet protocol is between 0xBAC0 and 0xBAC9
const DefaultPort = 0xBAC0 //47808

type udpDataLink struct {
	// netInterface                *net.Interface
	myAddress, broadcastAddress *btypes.Address
	port                        int
	// bcastPort is the UDP port used for broadcast traffic (Who-Is / I-Am).
	// Zero or negative falls back to DefaultPort (0xBAC0).
	bcastPort int
	listener  *net.UDPConn
}

/*
NewUDPDataLink returns udp listener
pass in your iface port by name, see an alternative NewUDPDataLinkFromIP if you wish to pass in by ip and subnet
  - inter: eth0
  - addr: 47808
*/
func NewUDPDataLink(inter string, port int) (link DataLink, err error) {
	if port == 0 {
		port = DefaultPort
	}
	addr := inter
	if !strings.ContainsRune(inter, '/') {
		addr, err = FindCIDRAddress(inter)
		if err != nil {
			return nil, err
		}
		fmt.Printf("find addr: %s -> %s\n", inter, addr)
	}
	link, err = dataLink(addr, port, "", 0)
	if err != nil {
		return nil, err
	}
	return link, nil
}

/*
NewUDPDataLinkFromInterfaceBound returns a server-side UDP listener bound to
the IPv4 address of the named interface and advertises broadcast (I-Am /
Who-Is) traffic on the interface's subnet-directed broadcast address using the
listening port.

inter accepts either an interface name ("eth0") or an "ip/prefix" string,
matching NewUDPDataLink's tolerance. Unlike NewUDPDataLink, failures (unknown
interface, no IPv4 address, bad CIDR) are returned to the caller: it never
silently falls back to binding 0.0.0.0.

NewUDPDataLinkFromInterfaceBound 返回服务端专用的 UDP 监听器：收窄绑定到指定
网卡的 IPv4 地址，广播发往该网卡所在子网的定向广播地址，广播端口 = 监听端口。
inter 可为网卡名（"eth0"）或 "ip/prefix"。网卡不存在 / 无 IPv4 / CIDR 解析失败
时返回 error，不静默回落 0.0.0.0。客户端仍须使用 NewUDPDataLink（INADDR_ANY）。
*/
func NewUDPDataLinkFromInterfaceBound(inter string, port int) (DataLink, error) {
	cidr := inter
	if !strings.ContainsRune(inter, '/') {
		var err error
		cidr, err = FindCIDRAddress(inter)
		if err != nil {
			return nil, err
		}
	}

	ip, _, err := net.ParseCIDR(cidr)
	if err != nil {
		return nil, fmt.Errorf("invalid interface address %q: %w", inter, err)
	}
	bindIP := ip.To4()
	if bindIP == nil {
		return nil, fmt.Errorf("interface address %q is not IPv4", cidr)
	}
	if bindIP.IsUnspecified() {
		return nil, fmt.Errorf("interface address %q resolves to unspecified IPv4", cidr)
	}

	// Reuse the existing bound dataLink path: narrow the bind to the interface
	// IPv4 and send broadcasts to the subnet-directed address on the listening
	// port.
	return dataLink(cidr, port, bindIP.String(), port)
}

func NewUDPDataLinkFromCIDR(cidr string, port int) (link DataLink, err error) {
	return dataLink(cidr, port, "", 0)
}

/*
NewUDPDataLinkFromIP returns udp listener
  - addr: 192.168.15.10
  - subNet: 24
  - addr: 47808
*/
func NewUDPDataLinkFromIP(addr string, subNet, port int) (link DataLink, err error) {
	addr = fmt.Sprintf("%s/%d", addr, subNet)
	link, err = dataLink(addr, port, "", 0)
	if err != nil {
		return nil, err
	}
	return link, nil
}

/*
NewUDPDataLinkFromIPBound returns a server-side udp listener that binds the
local socket to bindAddr and advertises broadcast (I-Am / Who-Is) traffic on
bcastPort.

  - addr: 192.168.15.10 (also the address used to derive the subnet broadcast)
  - subNet: 24
  - port: 47808 (listening port)
  - bcastPort: 47808 (0 or negative falls back to DefaultPort)

A bindAddr that is empty or unspecified (0.0.0.0) keeps the legacy wildcard
bind. Client-side constructors intentionally do NOT use this path: they must
keep binding INADDR_ANY to receive broadcast I-Am packets.

NewUDPDataLinkFromIPBound 返回服务端专用的 UDP 监听器：本地 socket 绑定到
bindAddr，广播（I-Am / Who-Is）使用 bcastPort。bindAddr 为空或 0.0.0.0 时
保留原有的通配绑定（回落）。客户端构造不使用此路径，必须继续绑定 INADDR_ANY
以接收广播 I-Am。
*/
func NewUDPDataLinkFromIPBound(addr string, subNet, port, bcastPort int) (link DataLink, err error) {
	cidr := fmt.Sprintf("%s/%d", addr, subNet)
	link, err = dataLink(cidr, port, addr, bcastPort)
	if err != nil {
		return nil, err
	}
	return link, nil
}

func dataLink(ipAddr string, port int, bindAddr string, bcastPort int) (DataLink, error) {
	if port == 0 {
		port = DefaultPort
	}

	ip, ipNet, err := net.ParseCIDR(ipAddr)
	if err != nil {
		return nil, err
	}

	broadcast := net.IP(make([]byte, 4))
	for i := range broadcast {
		broadcast[i] = ipNet.IP[i] | ^ipNet.Mask[i]
	}

	// bindIP is the local address the socket binds to. Empty/unspecified keeps
	// the legacy wildcard bind (INADDR_ANY); only the server-side bound
	// constructor narrows it to a configured address.
	// bindIP 为本地绑定地址；空/未指定保持原有通配绑定（INADDR_ANY），
	// 只有服务端绑定构造才收窄到配置地址。
	bindIP := net.IPv4(0, 0, 0, 0)
	if bindAddr != "" {
		parsed := net.ParseIP(bindAddr)
		if parsed == nil {
			return nil, fmt.Errorf("invalid bind address %q", bindAddr)
		}
		v4 := parsed.To4()
		if v4 == nil {
			return nil, fmt.Errorf("bind address %q is not IPv4", bindAddr)
		}
		if !v4.IsUnspecified() {
			bindIP = v4
		}
	}

	// If the endpoint IP is 0.0.0.0, use a real local IP for the source address
	// This ensures BACnet devices can respond to our NPDU source address
	actualIP := ip
	if !bindIP.IsUnspecified() {
		actualIP = bindIP
	} else if ip.IsUnspecified() {
		actualIP = GetLocalIP()
		if actualIP.IsUnspecified() {
			actualIP = ip // fallback to 0.0.0.0 if no local IP found
		}
	}

	// Yabe-style: the legacy client path binds to 0.0.0.0 (INADDR_ANY) to
	// receive broadcast IAm responses. The official Yabe tool binds to
	// INADDR_ANY so it can receive IAm packets sent to 255.255.255.255 (global
	// broadcast) by some BACnet simulators. The NPDU source address still uses
	// the actual IP for correct routing.
	// Yabe 风格：既有客户端路径绑定到 0.0.0.0 (INADDR_ANY) 以接收广播 IAm 响应。
	// 官方 Yabe 工具绑定到 INADDR_ANY，以便接收某些 BACnet 模拟器
	// 发送到 255.255.255.255（全局广播）的 IAm 包。
	// NPDU 源地址仍使用实际 IP 以保证正确路由。
	udpAddrStr := fmt.Sprintf("%s:%d", bindIP.String(), port)

	// Reusable bind: set SO_REUSEADDR so room-simulator can coexist with YABE on
	// the same Windows host. When bound to INADDR_ANY, broadcast Who-Is messages
	// and I-Am responses are correctly received regardless of destination address.
	lc := net.ListenConfig{
		Control: setReuseUDP,
	}

	conn, err := lc.ListenPacket(context.Background(), "udp4", udpAddrStr)
	if err != nil {
		return nil, fmt.Errorf("bind %s failed (is YABE or another BACnet app using this port?): %w", udpAddrStr, err)
	}

	udpConn := conn.(*net.UDPConn)

	//log.Printf("[DEBUG] UDP DataLink listening on %s:%d (Broadcast: %s)\n", ipAddr, port, broadcast.String())

	return &udpDataLink{
		listener:  udpConn,
		port:      port,
		bcastPort: bcastPort,
		myAddress: IPPortToAddress(actualIP, port),
		// Broadcast IP is computed from the subnet; the port and MacLen are
		// finalized in GetBroadcastAddress() (DefaultPort when bcastPort <= 0).
		broadcastAddress: IPPortToAddress(broadcast, port),
	}, nil
}

// Close shuts down the UDP listener. On Windows, closing a UDP socket does NOT
// interrupt a blocking ReadFromUDP call. SetReadDeadline to time.Now() forces
// ReadFromUDP to return immediately with a timeout error, allowing ClientRun()
// to exit cleanly. Without this, every ephemeral client scan leaks a goroutine
// and a UDP socket on Windows.
// Close 关闭 UDP 监听器。Windows 上关闭 UDP socket 不会中断阻塞的 ReadFromUDP 调用。
// 设置 SetReadDeadline 为当前时间可强制 ReadFromUDP 立即返回超时错误，
// 使 ClientRun() 干净退出。否则每次临时客户端扫描都会在 Windows 上泄漏一个 goroutine 和一个 UDP socket。
func (c *udpDataLink) Close() error {
	if c.listener != nil {
		// 强制中断 Windows 上阻塞的 ReadFromUDP，避免 goroutine/socket 泄漏
		_ = c.listener.SetReadDeadline(time.Now())
		return c.listener.Close()
	}
	return nil
}

func (c *udpDataLink) Receive(data []byte) (*btypes.Address, int, error) {
	n, adr, err := c.listener.ReadFromUDP(data)
	if err != nil {
		return nil, n, err
	}
	//log.Printf("[DEBUG] UDP Received %d bytes from %s\n", n, adr.String())
	adr.IP = adr.IP.To4()
	udpAddr := UDPToAddress(adr)
	return udpAddr, n, nil
}

func (c *udpDataLink) GetMyAddress() *btypes.Address {
	return c.myAddress
}

// GetBroadcastAddress returns the subnet broadcast address with the broadcast
// port and MacLen=0 to mark it as a broadcast address. bcastPort <= 0 keeps the
// standard BACnet port (47808); the server passes its listening port so I-Am
// announcements follow the port actually in use.
// The listening port may differ from the broadcast port to avoid SO_REUSEADDR
// conflicts when multiple BACnet entities share the same host.
func (c *udpDataLink) GetBroadcastAddress() *btypes.Address {
	addr := *c.broadcastAddress
	port := c.bcastPort
	if port <= 0 {
		port = DefaultPort
	}
	// Override port to the broadcast port
	if len(addr.Mac) >= 6 {
		addr.Mac[4] = uint8(port >> 8)
		addr.Mac[5] = uint8(port & 0xFF)
	}
	addr.MacLen = 0 // mark as broadcast address
	return &addr
}

func (c *udpDataLink) Send(data []byte, npdu *btypes.NPDU, dest *btypes.Address) (int, error) {
	// Get IP Address
	d, err := dest.UDPAddr()
	if err != nil {
		return 0, err
	}
	//log.Printf("[DEBUG] UDP Sending %d bytes to %s\n", len(data), d.String())
	return c.listener.WriteTo(data, &d)
}

// IPPortToAddress converts a given udp address into a bacnet address
func IPPortToAddress(ip net.IP, port int) *btypes.Address {
	return UDPToAddress(&net.UDPAddr{
		IP:   ip.To4(),
		Port: port,
	})
}

// UDPToAddress converts a given udp address into a bacnet address
func UDPToAddress(n *net.UDPAddr) *btypes.Address {
	a := &btypes.Address{}
	p := uint16(n.Port)
	// Length of IP plus the port
	length := net.IPv4len + 2
	a.Mac = make([]uint8, length)
	//Encode ip
	for i := 0; i < net.IPv4len; i++ {
		a.Mac[i] = n.IP[i]
	}
	// Encode port
	a.Mac[net.IPv4len+0] = uint8(p >> 8)
	a.Mac[net.IPv4len+1] = uint8(p & 0x00FF)

	a.MacLen = uint8(length)
	a.Adr = a.Mac
	a.Len = uint8(length)
	return a
}

// FindCIDRAddress find out CIDR address from net interface
func FindCIDRAddress(inter string) (string, error) {
	i, err := net.InterfaceByName(inter)
	if err != nil {
		return "", err
	}

	uni, err := i.Addrs()
	if err != nil {
		return "", err
	}

	if len(uni) == 0 {
		return "", fmt.Errorf("interface %s has no addresses", inter)
	}

	// Find the first IP4 ip
	for _, adr := range uni {
		IP, _, _ := net.ParseCIDR(adr.String())

		// To4 is non nil when the type is ip4
		if IP.To4() != nil {
			return adr.String(), nil
		}
	}
	// We couldn't find a interface or all of them are ip6
	return "", fmt.Errorf("no valid broadcasting address was found on interface %s", inter)
}
