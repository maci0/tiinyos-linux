package main

// The box is a USB network gadget (USB manufacturer "Tiiny") with a DHCP
// server on that link. A fresh link has no address until some DHCP
// client runs there. usbLinkWatch.watch looks for such a link every few
// seconds; when it has no IPv4 address it asks NetworkManager for a
// DHCP profile when NetworkManager runs (no password). Otherwise the
// link is reported as needs_admin on GET /pcsvr/usb-link; the app's UI
// (linux/main.js) explains and, if the user agrees, calls
// POST /pcsvr/usb-link/setup, which runs this binary as root through
// pkexec with -usb-dhcp to bring the link up, take the lease, assign
// the address and exit.

import (
	"context"
	"crypto/rand"
	"encoding/binary"
	"errors"
	"fmt"
	"log"
	"net"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"
	"unsafe"
)

const (
	tiinyManufacturer = "Tiiny"
	usbLinkPoll       = 3 * time.Second
	nmRuntimeDir      = "/run/NetworkManager"
	dhcpAttempts      = 4
	dhcpReplyWait     = 3 * time.Second
	dhcpClientPort    = 68
	dhcpServerPort    = 67
	ethPIP            = 0x0800
)

// usbLinkStatus is one Tiiny USB link as GET /pcsvr/usb-link reports it.
// State is configured, needs_admin (the UI should offer setup),
// configuring, or failed (Error says why; a declined prompt is failed).
type usbLinkStatus struct {
	Name    string `json:"name"`
	Index   int    `json:"index"`
	State   string `json:"state"`
	Attempt int    `json:"attempt"`
	Error   string `json:"error,omitempty"`
}

type usbLinkWatch struct {
	mu    sync.Mutex
	links map[int]*usbLinkStatus
}

func (u *usbLinkWatch) list() []usbLinkStatus {
	u.mu.Lock()
	defer u.mu.Unlock()
	out := make([]usbLinkStatus, 0, len(u.links))
	for _, link := range u.links {
		out = append(out, *link)
	}
	return out
}

// watch tracks Tiiny links. A link without IPv4 gets the NetworkManager
// profile once when NetworkManager runs; otherwise, or when that fails,
// it is reported as needs_admin for the UI to offer setupWithAdmin.
func (u *usbLinkWatch) watch(ctx context.Context) {
	tick := time.NewTicker(usbLinkPoll)
	defer tick.Stop()
	for {
		u.scan(tiinyLinks(), hasIPv4, networkManagerProfile)
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
		}
	}
}

// scan is one polling round over the current Tiiny links.
func (u *usbLinkWatch) scan(links []net.Interface, hasAddr func(net.Interface) bool, nm func(string) error) {
	seen := map[int]bool{}
	for _, iface := range links {
		seen[iface.Index] = true
		u.mu.Lock()
		link, known := u.links[iface.Index]
		if !known {
			link = &usbLinkStatus{Name: iface.Name, Index: iface.Index}
			u.links[iface.Index] = link
		}
		state := link.State
		u.mu.Unlock()
		switch {
		case hasAddr(iface):
			u.set(iface.Index, "configured", "")
		case known && state != "configured":
			// Configuring, or waiting for the user (needs_admin,
			// failed): no new attempt until the link changes.
		default:
			// A new link, or one that lost its address: one attempt.
			u.mu.Lock()
			link.Attempt++
			u.mu.Unlock()
			u.set(iface.Index, "configuring", "")
			if err := nm(iface.Name); err == nil {
				log.Printf("usb link %s: NetworkManager DHCP profile up", iface.Name)
				u.set(iface.Index, "configured", "")
			} else {
				log.Printf("usb link %s: %v; needs admin setup", iface.Name, err)
				u.set(iface.Index, "needs_admin", "")
			}
		}
	}
	u.mu.Lock()
	defer u.mu.Unlock()
	for index := range u.links {
		if !seen[index] {
			delete(u.links, index)
		}
	}
}

func (u *usbLinkWatch) set(index int, state, errText string) {
	u.mu.Lock()
	defer u.mu.Unlock()
	if link, ok := u.links[index]; ok {
		link.State, link.Error = state, errText
	}
}

// setupWithAdmin runs this binary as root through pkexec, which shows the
// desktop's polkit password dialog (no Go API exists for it); the root
// side (configureUSBLink) brings the link up and takes the DHCP lease.
func (u *usbLinkWatch) setupWithAdmin(name string) (usbLinkStatus, error) {
	var index int
	for _, iface := range tiinyLinks() {
		if iface.Name == name {
			index = iface.Index
		}
	}
	if index == 0 {
		return usbLinkStatus{Name: name, State: "failed", Error: "not a Tiiny USB link"}, errors.New("not a Tiiny USB link")
	}
	u.set(index, "configuring", "")
	self, err := os.Executable()
	if err == nil {
		var out []byte
		out, err = exec.Command("pkexec", self, "-usb-dhcp", name).CombinedOutput()
		if err != nil {
			err = fmt.Errorf("%v: %s", err, strings.TrimSpace(string(out)))
		} else {
			log.Printf("usb link %s: %s", name, strings.TrimSpace(string(out)))
		}
	}
	if err != nil {
		u.set(index, "failed", err.Error())
	} else {
		u.set(index, "configured", "")
	}
	u.mu.Lock()
	defer u.mu.Unlock()
	if link, ok := u.links[index]; ok {
		return *link, nil
	}
	return usbLinkStatus{Name: name, Index: index, State: "configured"}, nil
}

// tiinyLinks returns the network interfaces whose USB device reports
// manufacturer Tiiny.
func tiinyLinks() []net.Interface {
	ifaces, err := net.Interfaces()
	if err != nil {
		return nil
	}
	var out []net.Interface
	for _, iface := range ifaces {
		dev, err := filepath.EvalSymlinks(filepath.Join("/sys/class/net", iface.Name, "device"))
		if err != nil {
			continue
		}
		// device is the USB interface; its parent is the USB device.
		raw, err := os.ReadFile(filepath.Join(filepath.Dir(dev), "manufacturer"))
		if err == nil && strings.TrimSpace(string(raw)) == tiinyManufacturer {
			out = append(out, iface)
		}
	}
	return out
}

func hasIPv4(iface net.Interface) bool {
	addrs, _ := iface.Addrs()
	for _, addr := range addrs {
		if ipnet, ok := addr.(*net.IPNet); ok && ipnet.IP.To4() != nil {
			return true
		}
	}
	return false
}

// networkManagerProfile adds (or updates) a DHCP profile owned by this
// user and bound to the link's MAC, without the box's route or DNS, and
// brings it up. nmcli is used because Go has no NetworkManager client.
func networkManagerProfile(name string) error {
	if _, err := exec.LookPath("nmcli"); err != nil {
		return errors.New("NetworkManager is not installed")
	}
	if _, err := os.Stat(nmRuntimeDir); err != nil {
		return errors.New("NetworkManager is not running")
	}
	iface, err := net.InterfaceByName(name)
	if err != nil {
		return err
	}
	mac := iface.HardwareAddr.String()
	u, err := user.Current()
	if err != nil {
		return err
	}
	conn := "tiiny-usb-" + strings.ReplaceAll(mac, ":", "")
	settings := []string{
		"ethernet.mac-address", mac,
		"connection.permissions", "user:" + u.Username,
		"connection.autoconnect", "yes",
		"ipv4.method", "auto",
		"ipv4.addresses", "",
		"ipv4.never-default", "yes",
		"ipv4.ignore-auto-dns", "yes",
		"ipv6.method", "link-local",
	}
	args := append([]string{"connection", "modify", conn}, settings...)
	if exec.Command("nmcli", "-g", "connection.id", "connection", "show", conn).Run() != nil {
		args = append([]string{"connection", "add", "type", "ethernet", "con-name", conn}, settings...)
	}
	if out, err := exec.Command("nmcli", args...).CombinedOutput(); err != nil {
		return fmt.Errorf("%v: %s", err, strings.TrimSpace(string(out)))
	}
	if out, err := exec.Command("nmcli", "connection", "up", conn).CombinedOutput(); err != nil {
		return fmt.Errorf("%v: %s", err, strings.TrimSpace(string(out)))
	}
	return nil
}

// configureUSBLink runs as root. It refuses any interface that is not a
// Tiiny USB link, so the pkexec prompt cannot be used to reconfigure
// another interface.
func configureUSBLink(name string) error {
	var iface *net.Interface
	for _, link := range tiinyLinks() {
		if link.Name == name {
			iface = &link
			break
		}
	}
	if iface == nil {
		return fmt.Errorf("%s is not a Tiiny USB link", name)
	}
	if err := setLinkUp(name); err != nil {
		return fmt.Errorf("link up: %w", err)
	}
	lease, err := dhcpLease(iface)
	if err != nil {
		return err
	}
	if err := setIPv4(name, lease.addr, lease.mask); err != nil {
		return fmt.Errorf("address: %w", err)
	}
	ones, _ := net.IPMask(lease.mask.To4()).Size()
	fmt.Printf("%s %s/%d from %s\n", name, lease.addr, ones, lease.server)
	return nil
}

type ifreq struct {
	name [syscall.IFNAMSIZ]byte
	data [24]byte
}

func ioctlIfreq(name string, req uintptr, fill func(*ifreq)) (ifreq, error) {
	var r ifreq
	copy(r.name[:], name)
	if fill != nil {
		fill(&r)
	}
	fd, err := syscall.Socket(syscall.AF_INET, syscall.SOCK_DGRAM, 0)
	if err != nil {
		return r, err
	}
	defer syscall.Close(fd)
	if _, _, errno := syscall.Syscall(syscall.SYS_IOCTL, uintptr(fd), req, uintptr(unsafe.Pointer(&r))); errno != 0 {
		return r, errno
	}
	return r, nil
}

func setLinkUp(name string) error {
	r, err := ioctlIfreq(name, syscall.SIOCGIFFLAGS, nil)
	if err != nil {
		return err
	}
	flags := binary.NativeEndian.Uint16(r.data[:2]) | syscall.IFF_UP
	_, err = ioctlIfreq(name, syscall.SIOCSIFFLAGS, func(q *ifreq) { binary.NativeEndian.PutUint16(q.data[:2], flags) })
	return err
}

func setIPv4(name string, addr, mask net.IP) error {
	sockaddr := func(ip net.IP) func(*ifreq) {
		return func(q *ifreq) {
			binary.NativeEndian.PutUint16(q.data[:2], syscall.AF_INET)
			copy(q.data[4:8], ip.To4())
		}
	}
	if _, err := ioctlIfreq(name, syscall.SIOCSIFADDR, sockaddr(addr)); err != nil {
		return err
	}
	_, err := ioctlIfreq(name, syscall.SIOCSIFNETMASK, sockaddr(mask))
	return err
}

type lease struct {
	addr, mask, server net.IP
}

// dhcpLease runs DISCOVER/OFFER/REQUEST/ACK on an AF_PACKET socket, the
// only way to talk on a link that has no address yet.
func dhcpLease(iface *net.Interface) (lease, error) {
	fd, err := syscall.Socket(syscall.AF_PACKET, syscall.SOCK_DGRAM, int(htons(ethPIP)))
	if err != nil {
		return lease{}, err
	}
	defer syscall.Close(fd)
	ll := &syscall.SockaddrLinklayer{Protocol: htons(ethPIP), Ifindex: iface.Index}
	if err := syscall.Bind(fd, ll); err != nil {
		return lease{}, err
	}
	tv := syscall.NsecToTimeval(int64(dhcpReplyWait / 2))
	if err := syscall.SetsockoptTimeval(fd, syscall.SOL_SOCKET, syscall.SO_RCVTIMEO, &tv); err != nil {
		return lease{}, err
	}
	bcast := &syscall.SockaddrLinklayer{Protocol: htons(ethPIP), Ifindex: iface.Index, Halen: 6}
	copy(bcast.Addr[:], []byte{0xff, 0xff, 0xff, 0xff, 0xff, 0xff})
	var xid [4]byte
	if _, err := rand.Read(xid[:]); err != nil {
		return lease{}, err
	}
	for range dhcpAttempts {
		offer, ok := dhcpExchange(fd, bcast, dhcpMessage(1, xid, iface.HardwareAddr, nil, nil), xid, 2)
		if !ok {
			continue
		}
		req := dhcpMessage(3, xid, iface.HardwareAddr, offer.addr, offer.server)
		ack, ok := dhcpExchange(fd, bcast, req, xid, 5)
		if !ok {
			continue
		}
		if ack.mask == nil {
			ack.mask = offer.mask
		}
		if ack.mask == nil {
			return lease{}, errors.New("DHCP ACK without a subnet mask")
		}
		return ack, nil
	}
	return lease{}, fmt.Errorf("no DHCP answer on %s after %d attempts", iface.Name, dhcpAttempts)
}

func htons(v uint16) uint16 { return v<<8 | v>>8 }

// dhcpMessage builds a BOOTP request with the broadcast flag (op 1),
// wrapped in UDP 68->67 and IPv4 0.0.0.0->255.255.255.255.
func dhcpMessage(msgType byte, xid [4]byte, mac net.HardwareAddr, want, server net.IP) []byte {
	b := make([]byte, 240)
	b[0], b[1], b[2] = 1, 1, 6
	copy(b[4:8], xid[:])
	binary.BigEndian.PutUint16(b[10:12], 0x8000)
	copy(b[28:34], mac)
	copy(b[236:240], []byte{99, 130, 83, 99})
	b = append(b, 53, 1, msgType)
	if want != nil {
		b = append(b, 50, 4)
		b = append(b, want.To4()...)
	}
	if server != nil {
		b = append(b, 54, 4)
		b = append(b, server.To4()...)
	}
	b = append(b, 55, 3, 1, 3, 51, 255)
	udp := make([]byte, 8, 8+len(b))
	binary.BigEndian.PutUint16(udp[0:2], dhcpClientPort)
	binary.BigEndian.PutUint16(udp[2:4], dhcpServerPort)
	binary.BigEndian.PutUint16(udp[4:6], uint16(8+len(b)))
	udp = append(udp, b...)
	ip := make([]byte, 20, 20+len(udp))
	ip[0], ip[8], ip[9] = 0x45, 64, syscall.IPPROTO_UDP
	binary.BigEndian.PutUint16(ip[2:4], uint16(20+len(udp)))
	copy(ip[16:20], net.IPv4bcast.To4())
	binary.BigEndian.PutUint16(ip[10:12], ipChecksum(ip))
	return append(ip, udp...)
}

func ipChecksum(h []byte) uint16 {
	var sum uint32
	for i := 0; i+1 < len(h); i += 2 {
		sum += uint32(binary.BigEndian.Uint16(h[i : i+2]))
	}
	for sum > 0xffff {
		sum = sum>>16 + sum&0xffff
	}
	return ^uint16(sum)
}

// dhcpExchange sends msg and waits for a reply of type want (2 OFFER,
// 5 ACK) with the same xid. A NAK or silence returns false.
func dhcpExchange(fd int, to *syscall.SockaddrLinklayer, msg []byte, xid [4]byte, want byte) (lease, bool) {
	if err := syscall.Sendto(fd, msg, 0, to); err != nil {
		return lease{}, false
	}
	buf := make([]byte, 1500)
	deadline := time.Now().Add(dhcpReplyWait)
	for time.Now().Before(deadline) {
		n, _, err := syscall.Recvfrom(fd, buf, 0)
		if err != nil {
			continue
		}
		if got, ok := parseDHCPReply(buf[:n], xid); ok {
			return got.lease, got.msgType == want
		}
	}
	return lease{}, false
}

type dhcpReply struct {
	lease
	msgType byte
}

// parseDHCPReply reads an IPv4/UDP packet to port 68 carrying a BOOTP
// reply for xid. Anything else returns false.
func parseDHCPReply(p []byte, xid [4]byte) (dhcpReply, bool) {
	if len(p) < 20 || p[0]>>4 != 4 || p[9] != syscall.IPPROTO_UDP {
		return dhcpReply{}, false
	}
	ihl := int(p[0]&0x0f) * 4
	if len(p) < ihl+8 || binary.BigEndian.Uint16(p[ihl+2:ihl+4]) != dhcpClientPort {
		return dhcpReply{}, false
	}
	b := p[ihl+8:]
	if len(b) < 240 || b[0] != 2 || [4]byte(b[4:8]) != xid || [4]byte(b[236:240]) != [4]byte{99, 130, 83, 99} {
		return dhcpReply{}, false
	}
	r := dhcpReply{addr: net.IP(append([]byte(nil), b[16:20]...))}
	for i := 240; i < len(b) && b[i] != 255; {
		if b[i] == 0 {
			i++
			continue
		}
		if i+1 >= len(b) || i+2+int(b[i+1]) > len(b) {
			return dhcpReply{}, false
		}
		code, val := b[i], b[i+2:i+2+int(b[i+1])]
		switch {
		case code == 53 && len(val) == 1:
			r.msgType = val[0]
		case code == 1 && len(val) == 4:
			r.mask = net.IP(append([]byte(nil), val...))
		case code == 54 && len(val) == 4:
			r.server = net.IP(append([]byte(nil), val...))
		}
		i += 2 + len(val)
	}
	return r, r.msgType != 0
}
