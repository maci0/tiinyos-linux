// Linux stand-in for the Tiiny PC service (pcsvr).
//
// TiinyOS looks up names such as api.tiiny, then opens
// ws://127.0.0.1:60000/devices_list/ws and waits for one
// "connection" message. The Windows and macOS builds ship a
// closed Go service that does both. This process does the same
// two jobs on Linux: answer those names from 127.0.0.1:60053,
// and publish devices found by the gadget discovery protocol
// (UDP 39217, token GADGET_DISCOVER_V1, or HTTP :39218/device.json).
package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"
)

const (
	defaultAPIHost      = "127.0.0.1"
	defaultAPIPort      = 60000
	defaultDNSPort      = 60053
	defaultDiscoverUDP  = 39217
	defaultDiscoverHTTP = 39218
	defaultDevicePath   = "/device.json"
	defaultToken        = "GADGET_DISCOVER_V1"
	defaultDomain       = "tiiny.local"
	defaultDeviceTTL    = 8 * time.Second
	discoverEvery       = time.Second
	responseWindow      = 800 * time.Millisecond
	httpDiscoverTimeout = time.Second
	sshListenPort       = 60022
	deviceSSHPort       = 22
	deviceHTTPPort      = 80
	maxBody             = 1 << 20
	dnsTTL              = 60
	bindRetryFor        = 65 * time.Second
	bindRetryEvery      = 250 * time.Millisecond
)

func main() {
	configPath := flag.String("f", "pcsvr-api.yaml", "service config (port and discovery settings are read)")
	portFile := flag.String("port-file", "", "write the bound HTTP port here (for Port: 0, a free port)")
	usbDHCP := flag.String("usb-dhcp", "", "as root (pkexec): bring up this Tiiny USB link and take its DHCP lease, then exit")
	apiNamesSetup := flag.String("api-names", "", "as root (pkexec): point the API names in /etc/hosts at this box address, then exit")
	flag.Parse()
	if *usbDHCP != "" {
		if err := configureUSBLink(*usbDHCP); err != nil {
			log.Fatal(err)
		}
		return
	}

	if *apiNamesSetup != "" {
		if err := configureAPINames(*apiNamesSetup); err != nil {
			log.Fatal(err)
		}
		return
	}

	cfg := loadConfig(*configPath)
	listener, err := listenRetry(func() (net.Listener, error) {
		return net.Listen("tcp", net.JoinHostPort(cfg.Host, strconv.Itoa(cfg.Port)))
	})
	if err != nil {
		log.Fatal(err)
	}
	port := listener.Addr().(*net.TCPAddr).Port
	log.Printf("pcsvr linux %s listening on %s:%d", versionString(), cfg.Host, port)
	if *portFile != "" {
		if err := os.WriteFile(*portFile, []byte(strconv.Itoa(port)+"\n"), 0o644); err != nil {
			log.Fatal(err)
		}
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	state := newState(cfg)
	if cfg.Discover {
		go state.discoverLoop(ctx)
	}
	if cfg.DNS {
		go serveDNS(ctx, cfg, state)
	}
	if cfg.SSH {
		go serveSSH(ctx, cfg, state)
	}
	if cfg.APINames {
		go state.apiNames.watch(ctx, state.targetIP)
	}
	if cfg.USBLink {
		go state.usb.watch(ctx)
	}

	mux := http.NewServeMux()
	register(mux, state)
	handler := proxyOrLocal(mux, func() string {
		ip := state.targetIP()
		if ip == "" {
			return ""
		}
		return net.JoinHostPort(ip, strconv.Itoa(deviceHTTPPort))
	})
	server := &http.Server{
		Handler:           handler,
		ReadHeaderTimeout: 5 * time.Second,
	}
	go func() {
		<-ctx.Done()
		shut, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		_ = server.Shutdown(shut)
	}()
	err = server.Serve(listener)
	if err != nil && !errors.Is(err, http.ErrServerClosed) {
		log.Fatal(err)
	}
}

type config struct {
	Host         string
	Port         int
	DNSPort      int
	DiscoverUDP  int
	DiscoverHTTP int
	DevicePath   string
	Token        string
	Domain       string
	DeviceTTL    time.Duration
	Discover     bool
	DNS          bool
	SSH          bool
	USBLink      bool
	APINames     bool
}

func loadConfig(path string) config {
	cfg := config{
		Host:         defaultAPIHost,
		Port:         defaultAPIPort,
		DNSPort:      defaultDNSPort,
		DiscoverUDP:  defaultDiscoverUDP,
		DiscoverHTTP: defaultDiscoverHTTP,
		DevicePath:   defaultDevicePath,
		Token:        defaultToken,
		Domain:       defaultDomain,
		DeviceTTL:    defaultDeviceTTL,
		Discover:     true,
		DNS:          true,
		SSH:          true,
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		log.Printf("config %s unreadable (%v); using defaults", path, err)
		return cfg
	}
	// The shipped file is a small go-zero yaml. A few integer and string
	// fields are enough; the rest of the document is ignored.
	section := ""
	for _, line := range strings.Split(string(raw), "\n") {
		trim := strings.TrimSpace(line)
		if trim == "" || strings.HasPrefix(trim, "#") {
			continue
		}
		if !strings.HasPrefix(line, " ") && !strings.HasPrefix(line, "\t") && strings.HasSuffix(trim, ":") && !strings.Contains(trim, " ") {
			section = strings.TrimSuffix(trim, ":")
			continue
		}
		key, val, ok := strings.Cut(trim, ":")
		if !ok {
			continue
		}
		key = strings.TrimSpace(key)
		val = strings.Trim(strings.TrimSpace(val), `"'`)
		switch section + "." + key {
		case ".Host":
			cfg.Host = val
		case ".Port":
			cfg.Port = atoi(val, cfg.Port)
		case "DNS.ListenPort":
			cfg.DNSPort = atoi(val, cfg.DNSPort)
		case "DNS.Domain":
			cfg.Domain = strings.TrimSuffix(val, ".")
		case "DeviceDiscovery.UDPPort":
			cfg.DiscoverUDP = atoi(val, cfg.DiscoverUDP)
		case "DeviceDiscovery.HTTPPort":
			cfg.DiscoverHTTP = atoi(val, cfg.DiscoverHTTP)
		case "DeviceDiscovery.HTTPPath":
			cfg.DevicePath = val
		case "DeviceDiscovery.RequestToken":
			cfg.Token = val
		case "DeviceDiscovery.DeviceTTLSeconds":
			cfg.DeviceTTL = time.Duration(atoi(val, 8)) * time.Second
		case "DeviceDiscovery.Enabled":
			cfg.Discover = val != "false"
		case "DNS.Enabled":
			cfg.DNS = val != "false"
		case "SSHProxy.Enabled":
			cfg.SSH = val != "false"
		case "APINames.Enabled":
			cfg.APINames = val == "true"
		case "USBLink.Enabled":
			cfg.USBLink = val == "true"
		}
	}
	return cfg
}

func atoi(s string, fallback int) int {
	n, err := strconv.Atoi(s)
	if err != nil {
		return fallback
	}
	return n
}

func versionString() string {
	return "1.0.3-linux"
}

// device is one Tiiny box, possibly reachable on USB and on the LAN.
type device struct {
	SchemaVersion  string      `json:"schema_version,omitempty"`
	DeviceName     string      `json:"device_name"`
	DeviceID       string      `json:"device_id"`
	SerialNumber   string      `json:"serial_number,omitempty"`
	Hostname       string      `json:"hostname,omitempty"`
	DiscoveryToken string      `json:"discovery_token,omitempty"`
	IPv4Addresses  []ipOnIface `json:"ipv4_addresses,omitempty"`
	USB            *usbLink    `json:"usb,omitempty"`
	Service        *gadgetSvc  `json:"service,omitempty"`
	seen           time.Time
	source         string
}

type ipOnIface struct {
	Interface string `json:"interface"`
	Address   string `json:"address"`
}

type usbLink struct {
	Interface string `json:"interface"`
	Active    int    `json:"active"`
	Network   string `json:"network,omitempty"`
	DeviceIP  string `json:"device_ip"`
	HostIP    string `json:"host_ip,omitempty"`
}

type gadgetSvc struct {
	HTTPPort int    `json:"http_port"`
	HTTPPath string `json:"http_path"`
}

// published is the shape TiinyOS reads off the devices_list socket.
type published struct {
	DeviceID     string   `json:"device_id"`
	DeviceName   string   `json:"device_name,omitempty"`
	DeviceIP     string   `json:"device_ip"`
	Local        bool     `json:"local"`
	USB          bool     `json:"usb"`
	Reachable    bool     `json:"reachable"`
	Interface    string   `json:"interface,omitempty"`
	SerialNumber string   `json:"serial_number,omitempty"`
	IPv4         []string `json:"-"`
	MetadataURL  string   `json:"-"`
}

type snapshot struct {
	Event string `json:"event"`
	Data  struct {
		Status         string      `json:"status"`
		Devices        []published `json:"devices"`
		LocalDevices   []published `json:"local_devices"`
		SelectedDevice *published  `json:"selected_device"`
	} `json:"data"`
}

// wsEvent is the two-field envelope writeDevicesListWSEvent builds.
// Data is a device item for device_added, device_updated, and
// device_unreachable, and a selected device for device_selected.
type wsEvent struct {
	Event string `json:"event"`
	Data  any    `json:"data"`
}

// deviceEvent is the 184-byte item those three events carry.
// Fields the Linux publisher does not have stay empty. omitempty
// matches the recovered tags on the last four fields.
type deviceEvent struct {
	DeviceID               string   `json:"device_id"`
	DeviceIP               string   `json:"device_ip"`
	IPv4                   []string `json:"ipv4"`
	DeviceName             string   `json:"device_name"`
	MetadataURL            string   `json:"metadata_url"`
	LastSeenAt             string   `json:"last_seen_at"`
	Reachable              bool     `json:"reachable"`
	USB                    bool     `json:"usb"`
	Local                  bool     `json:"local"`
	ConnectionType         string   `json:"connection_type"`
	OSUSBEventAt           string   `json:"os_usb_event_at,omitempty"`
	USBAppearanceLatencyMS int64    `json:"usb_appearance_latency_ms,omitempty"`
	ChangedFields          []string `json:"changed_fields,omitempty"`
	Previous               any      `json:"previous,omitempty"`
}

// selectedEvent is the 64-byte device_selected value. selectedDevicesEqual
// compares device_id and the local byte. user_info stays nil.
type selectedEvent struct {
	DeviceID   string `json:"device_id"`
	DeviceIP   string `json:"device_ip"`
	DeviceName string `json:"device_name"`
	Local      bool   `json:"local"`
	Reachable  bool   `json:"reachable"`
	UserInfo   any    `json:"user_info,omitempty"`
}

type state struct {
	cfg      config
	mu       sync.Mutex
	found    map[string]*device
	selected string
	authKeys map[string]string
	clients  map[chan []wsEvent]struct{}
	wsPrev   snapshot
	svc      *serviceState
	usb      *usbLinkWatch
	apiNames *apiNamesState
}

func newState(cfg config) *state {
	return &state{
		cfg:      cfg,
		found:    map[string]*device{},
		authKeys: map[string]string{},
		clients:  map[chan []wsEvent]struct{}{},
		usb:      &usbLinkWatch{links: map[int]*usbLinkStatus{}},
		apiNames: &apiNamesState{},
	}
}

// devicesList is DevicesListResp. The fields are status, error_code,
// device_count, selected_device, local_devices, devices, user_info,
// and msg. The websocket frame is a different object: it keeps event.
func (s *state) devicesList() map[string]any {
	snap := s.current()
	return map[string]any{
		"status":          snap.Data.Status,
		"error_code":      0,
		"device_count":    len(snap.Data.Devices) + len(snap.Data.LocalDevices),
		"selected_device": snap.Data.SelectedDevice,
		"local_devices":   snap.Data.LocalDevices,
		"devices":         snap.Data.Devices,
		"user_info":       nil,
		"msg":             "",
	}
}

func (s *state) current() snapshot {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.currentLocked()
}

func (s *state) currentLocked() snapshot {
	now := time.Now()
	var snap snapshot
	snap.Event = "connection"
	snap.Data.Status = "ok"
	var selected *published
	for id, dev := range s.found {
		if now.Sub(dev.seen) > s.cfg.DeviceTTL {
			delete(s.found, id)
			continue
		}
		pub := publish(dev)
		if pub.DeviceIP == "" {
			continue
		}
		if pub.Local {
			snap.Data.LocalDevices = append(snap.Data.LocalDevices, pub)
		} else {
			snap.Data.Devices = append(snap.Data.Devices, pub)
		}
		if id == s.selected {
			copy := pub
			selected = &copy
		}
	}
	if selected == nil && len(snap.Data.LocalDevices)+len(snap.Data.Devices) == 1 {
		var only published
		if len(snap.Data.LocalDevices) == 1 {
			only = snap.Data.LocalDevices[0]
		} else {
			only = snap.Data.Devices[0]
		}
		selected = &only
		s.selected = only.DeviceID
	}
	snap.Data.SelectedDevice = selected
	if snap.Data.Devices == nil {
		snap.Data.Devices = []published{}
	}
	if snap.Data.LocalDevices == nil {
		snap.Data.LocalDevices = []published{}
	}
	return snap
}

func publish(dev *device) published {
	usbIP := ""
	usbIface := ""
	if dev.USB != nil && dev.USB.Active != 0 && dev.USB.DeviceIP != "" && hostHasIP(dev.USB.HostIP) {
		usbIP = dev.USB.DeviceIP
		usbIface = dev.USB.Interface
	}
	lanIP := ""
	lanIface := ""
	for _, addr := range dev.IPv4Addresses {
		if addr.Address == "" || addr.Address == dev.usbDeviceIP() {
			continue
		}
		lanIP = addr.Address
		lanIface = addr.Interface
		break
	}
	useUSB := usbIP != ""
	ip := usbIP
	iface := usbIface
	if !useUSB {
		ip = lanIP
		iface = lanIface
	}
	if ip == "" && len(dev.IPv4Addresses) > 0 {
		ip = dev.IPv4Addresses[0].Address
		iface = dev.IPv4Addresses[0].Interface
	}
	var ipv4 []string
	if useUSB && usbIP != "" {
		ipv4 = append(ipv4, usbIP)
	}
	for _, addr := range dev.IPv4Addresses {
		if addr.Address == "" {
			continue
		}
		seen := false
		for _, have := range ipv4 {
			if have == addr.Address {
				seen = true
				break
			}
		}
		if !seen {
			ipv4 = append(ipv4, addr.Address)
		}
	}
	if len(ipv4) == 0 && ip != "" {
		ipv4 = []string{ip}
	}
	return published{
		DeviceID:     dev.DeviceID,
		DeviceName:   dev.DeviceName,
		DeviceIP:     ip,
		Local:        useUSB,
		USB:          useUSB,
		Reachable:    ip != "",
		Interface:    iface,
		SerialNumber: dev.SerialNumber,
		IPv4:         ipv4,
		MetadataURL:  metadataURL(ip),
	}
}

// metadataURL is the 21-byte format "http://%s/device.json". The host
// is the selected address plus the discovery port.
func metadataURL(ip string) string {
	if ip == "" {
		return ""
	}
	return "http://" + net.JoinHostPort(ip, strconv.Itoa(defaultDiscoverHTTP)) + defaultDevicePath
}

func (dev *device) usbDeviceIP() string {
	if dev.USB == nil {
		return ""
	}
	return dev.USB.DeviceIP
}

func hostHasIP(ip string) bool {
	if ip == "" {
		return false
	}
	ifaces, err := net.Interfaces()
	if err != nil {
		return false
	}
	for _, iface := range ifaces {
		addrs, _ := iface.Addrs()
		for _, addr := range addrs {
			ipnet, ok := addr.(*net.IPNet)
			if ok && ipnet.IP.String() == ip {
				return true
			}
		}
	}
	return false
}

func (s *state) targetIP() string {
	snap := s.current()
	if snap.Data.SelectedDevice != nil {
		return snap.Data.SelectedDevice.DeviceIP
	}
	if len(snap.Data.LocalDevices) > 0 {
		return snap.Data.LocalDevices[0].DeviceIP
	}
	if len(snap.Data.Devices) > 0 {
		return snap.Data.Devices[0].DeviceIP
	}
	return ""
}

func (s *state) subscribe() (chan []wsEvent, func()) {
	ch := make(chan []wsEvent, 16)
	s.mu.Lock()
	s.clients[ch] = struct{}{}
	s.mu.Unlock()
	return ch, func() {
		s.mu.Lock()
		delete(s.clients, ch)
		s.mu.Unlock()
	}
}

func (s *state) publishChange() {
	s.mu.Lock()
	defer s.mu.Unlock()
	for {
		prev := s.wsPrev
		next := s.currentLocked()
		events := diffDeviceEvents(prev, next)
		s.wsPrev = next
		if len(events) == 0 {
			return
		}
		for ch := range s.clients {
			select {
			case ch <- events:
			default:
			}
		}
		// currentLocked can record an auto-selected device while the
		// snapshot is built. A second pass emits device_selected once
		// that stored id is part of the baseline.
		if selectedEqual(asSelected(prev.Data.SelectedDevice), asSelected(next.Data.SelectedDevice)) {
			return
		}
	}
}

func deviceChanged(old, next published) bool {
	return old.DeviceIP != next.DeviceIP || old.Local != next.Local || old.DeviceName != next.DeviceName || old.Reachable != next.Reachable || old.MetadataURL != next.MetadataURL || !sameStrings(old.IPv4, next.IPv4)
}

func sameStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func deviceItems(snap snapshot) []published {
	return append(append([]published{}, snap.Data.LocalDevices...), snap.Data.Devices...)
}

func asDeviceEvent(p published) deviceEvent {
	kind := "network"
	if p.Local || p.USB {
		kind = "usb"
	}
	ipv4 := p.IPv4
	if ipv4 == nil {
		ipv4 = []string{}
	}
	return deviceEvent{
		DeviceID:       p.DeviceID,
		DeviceIP:       p.DeviceIP,
		IPv4:           ipv4,
		DeviceName:     p.DeviceName,
		MetadataURL:    p.MetadataURL,
		LastSeenAt:     "",
		Reachable:      p.Reachable,
		USB:            p.USB,
		Local:          p.Local,
		ConnectionType: kind,
	}
}

func asSelected(p *published) *selectedEvent {
	if p == nil {
		return nil
	}
	return &selectedEvent{
		DeviceID:   p.DeviceID,
		DeviceIP:   p.DeviceIP,
		DeviceName: p.DeviceName,
		Local:      p.Local,
		Reachable:  p.Reachable,
	}
}

// selectedEqual is selectedDevicesEqual: both nil, or the same
// device_id and the same local byte.
func selectedEqual(a, b *selectedEvent) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	return a.DeviceID == b.DeviceID && a.Local == b.Local
}

// diffDeviceEvents is the three slices the devices websocket walks.
// A device id that is new is device_added. An id that is gone is
// device_unreachable. An id whose local byte changed is
// device_updated. A selected device that fails selectedDevicesEqual
// is device_selected.
func diffDeviceEvents(prev, next snapshot) []wsEvent {
	before := map[string]published{}
	for _, item := range deviceItems(prev) {
		before[item.DeviceID] = item
	}
	after := map[string]published{}
	for _, item := range deviceItems(next) {
		after[item.DeviceID] = item
	}
	var added, updated, gone []published
	changed := map[string][]string{}
	for _, item := range deviceItems(next) {
		old, ok := before[item.DeviceID]
		if !ok {
			added = append(added, item)
			continue
		}
		// devicesListDeviceChangedFields compares device_ip, ipv4,
		// device_name, metadata_url, and reachable. ipv4 and
		// metadata_url are empty on this publisher, so a change is
		// one of the three fields it fills. local is what moves a
		// device between the two lists, and that is device_updated.
		var fields []string
		if old.DeviceIP != item.DeviceIP {
			fields = append(fields, "device_ip")
		}
		if !sameStrings(old.IPv4, item.IPv4) {
			fields = append(fields, "ipv4")
		}
		if old.DeviceName != item.DeviceName {
			fields = append(fields, "device_name")
		}
		if old.MetadataURL != item.MetadataURL {
			fields = append(fields, "metadata_url")
		}
		if old.Reachable != item.Reachable {
			fields = append(fields, "reachable")
		}
		if old.Local != item.Local || len(fields) > 0 {
			updated = append(updated, item)
			changed[item.DeviceID] = fields
		}
	}
	for _, item := range deviceItems(prev) {
		if _, ok := after[item.DeviceID]; !ok {
			gone = append(gone, item)
		}
	}
	out := make([]wsEvent, 0, len(added)+len(updated)+len(gone)+1)
	for _, item := range added {
		out = append(out, wsEvent{Event: "device_added", Data: asDeviceEvent(item)})
	}
	for _, item := range updated {
		ev := asDeviceEvent(item)
		ev.ChangedFields = changed[item.DeviceID]
		out = append(out, wsEvent{Event: "device_updated", Data: ev})
	}
	for _, item := range gone {
		out = append(out, wsEvent{Event: "device_unreachable", Data: asDeviceEvent(item)})
	}
	sel := asSelected(next.Data.SelectedDevice)
	if !selectedEqual(asSelected(prev.Data.SelectedDevice), sel) && sel != nil {
		out = append(out, wsEvent{Event: "device_selected", Data: sel})
	}
	return out
}

func (s *state) observe(dev *device) {
	if dev.DeviceID == "" {
		return
	}
	dev.seen = time.Now()
	s.mu.Lock()
	prev := s.found[dev.DeviceID]
	s.found[dev.DeviceID] = dev
	changed := prev == nil || deviceChanged(publish(prev), publish(dev))
	s.mu.Unlock()
	if changed {
		log.Printf("device %s (%s) at usb=%v", dev.DeviceID, dev.DeviceName, dev.USB != nil && dev.USB.DeviceIP != "")
		s.publishChange()
	}
}

func (s *state) discoverLoop(ctx context.Context) {
	tick := time.NewTicker(discoverEvery)
	defer tick.Stop()
	s.probe(ctx)
	for {
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
			s.probe(ctx)
		}
	}
}

func (s *state) probe(ctx context.Context) {
	s.probeUDP(ctx)
	s.probeUSBHTTP(ctx)
}

func (s *state) probeUDP(ctx context.Context) {
	conn, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.IPv4zero, Port: 0})
	if err != nil {
		return
	}
	defer conn.Close()
	_ = conn.SetReadDeadline(time.Now().Add(responseWindow))
	targets := []string{net.IPv4bcast.String()}
	ifaces, _ := net.Interfaces()
	for _, iface := range ifaces {
		if iface.Flags&net.FlagUp == 0 || iface.Flags&net.FlagBroadcast == 0 {
			continue
		}
		addrs, _ := iface.Addrs()
		for _, addr := range addrs {
			ipnet, ok := addr.(*net.IPNet)
			if !ok || ipnet.IP.To4() == nil {
				continue
			}
			bcast := broadcast(ipnet)
			if bcast != nil {
				targets = append(targets, bcast.String())
			}
		}
	}
	seen := map[string]struct{}{}
	for _, target := range targets {
		if _, ok := seen[target]; ok {
			continue
		}
		seen[target] = struct{}{}
		dst, err := net.ResolveUDPAddr("udp4", net.JoinHostPort(target, strconv.Itoa(s.cfg.DiscoverUDP)))
		if err != nil {
			continue
		}
		_, _ = conn.WriteToUDP([]byte(s.cfg.Token), dst)
	}
	buf := make([]byte, 64*1024)
	deadline := time.Now().Add(responseWindow)
	for time.Now().Before(deadline) {
		_ = conn.SetReadDeadline(deadline)
		n, _, err := conn.ReadFromUDP(buf)
		if err != nil {
			return
		}
		dev, ok := parseDevice(buf[:n])
		if ok {
			dev.source = "udp"
			s.observe(dev)
		}
	}
	_ = ctx
}

func broadcast(n *net.IPNet) net.IP {
	ip := n.IP.To4()
	mask := net.IP(n.Mask).To4()
	if ip == nil || mask == nil {
		return nil
	}
	out := make(net.IP, 4)
	for i := 0; i < 4; i++ {
		out[i] = ip[i] | ^mask[i]
	}
	return out
}

func (s *state) probeUSBHTTP(ctx context.Context) {
	ifaces, err := net.Interfaces()
	if err != nil {
		return
	}
	client := &http.Client{Timeout: httpDiscoverTimeout}
	for _, iface := range ifaces {
		if iface.Flags&net.FlagUp == 0 {
			continue
		}
		addrs, _ := iface.Addrs()
		var v4 []*net.IPNet
		for _, addr := range addrs {
			ipnet, ok := addr.(*net.IPNet)
			if ok && ipnet.IP.To4() != nil {
				v4 = append(v4, &net.IPNet{IP: ipnet.IP.To4(), Mask: ipnet.Mask})
			}
		}
		// A CDC NCM gadget link can be up before any address is assigned.
		// The device still answers device.json on the link-local range
		// the gadget uses, so probe that from the interface itself.
		if len(v4) == 0 && isGadgetLink(iface.Name) {
			s.probeGadgetLink(ctx, client, iface.Name)
			continue
		}
		for _, ipnet := range v4 {
			peer := pointToPointPeer(ipnet)
			if peer == nil || !isGadgetLink(iface.Name) && !isUSBIface(iface.Name) {
				continue
			}
			dev, ok := s.fetchDevice(ctx, client, peer.String())
			if !ok {
				continue
			}
			if dev.USB == nil {
				dev.USB = &usbLink{Interface: iface.Name, Active: 1, DeviceIP: peer.String(), HostIP: ipnet.IP.String()}
			}
			s.observe(dev)
		}
	}
}

func (s *state) probeGadgetLink(ctx context.Context, client *http.Client, name string) {
	// The link is up and has no address yet. These are the addresses a
	// CDC NCM gadget answers on before the host is configured.
	for _, peer := range []string{"169.254.0.2", "169.254.1.2", "192.168.7.2", "172.17.7.177"} {
		dev, ok := s.fetchDevice(ctx, client, peer)
		if !ok {
			continue
		}
		if dev.USB == nil {
			dev.USB = &usbLink{Interface: name, Active: 1, DeviceIP: peer}
		}
		s.observe(dev)
		return
	}
}

func (s *state) fetchDevice(ctx context.Context, client *http.Client, peer string) (*device, bool) {
	url := fmt.Sprintf("http://%s:%d%s", peer, s.cfg.DiscoverHTTP, s.cfg.DevicePath)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, false
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, false
	}
	body, _ := io.ReadAll(io.LimitReader(resp.Body, maxBody))
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, false
	}
	dev, ok := parseDevice(body)
	if !ok {
		return nil, false
	}
	dev.source = "usb-http"
	return dev, true
}

func isGadgetLink(name string) bool {
	raw, err := os.ReadFile("/sys/class/net/" + name + "/device/uevent")
	if err != nil {
		return false
	}
	text := string(raw)
	return strings.Contains(text, "cdc_ncm") || strings.Contains(text, "cdc_ether") || strings.Contains(text, "rndis")
}

func isUSBIface(name string) bool {
	switch {
	case strings.HasPrefix(name, "enx"),
		strings.HasPrefix(name, "usb"),
		strings.HasPrefix(name, "eth"),
		strings.HasPrefix(name, "enp"):
		// A /30 on one of these is what the gadget uses. The caller
		// still requires a point-to-point peer, so a normal LAN eth
		// with a /24 does not match.
		return true
	default:
		return false
	}
}

func pointToPointPeer(n *net.IPNet) net.IP {
	ones, bits := n.Mask.Size()
	ip := n.IP.To4()
	if ip == nil || bits != 32 || ones != 30 {
		return nil
	}
	network := ip.Mask(n.Mask)
	for host := byte(1); host <= 2; host++ {
		cand := make(net.IP, 4)
		copy(cand, network)
		cand[3] = network[3] | host
		if !cand.Equal(ip) {
			return cand
		}
	}
	return nil
}

func parseDevice(raw []byte) (*device, bool) {
	var dev device
	if err := json.Unmarshal(raw, &dev); err != nil {
		return nil, false
	}
	if dev.DeviceID == "" {
		var loose map[string]any
		if json.Unmarshal(raw, &loose) != nil {
			return nil, false
		}
		dev.DeviceID = firstString(loose, "device_id", "id", "serial_number", "sn", "serial")
		dev.DeviceName = firstString(loose, "device_name", "name", "hostname")
		dev.SerialNumber = firstString(loose, "serial_number", "sn", "serial")
	}
	if dev.DeviceID == "" {
		return nil, false
	}
	// /api/v1/connect answers with the serial as device_id. The app
	// rejects the connection when the selected id differs, so the
	// published id is the serial when the box has one.
	if dev.SerialNumber != "" {
		dev.DeviceID = dev.SerialNumber
	}
	if dev.DeviceName == "" {
		dev.DeviceName = dev.DeviceID
	}
	return &dev, true
}

func firstString(m map[string]any, keys ...string) string {
	for _, key := range keys {
		v, ok := m[key]
		if !ok {
			continue
		}
		s, ok := v.(string)
		if ok && s != "" {
			return s
		}
	}
	return ""
}

func register(mux *http.ServeMux, s *state) {
	mux.HandleFunc("GET /pcsvr/usb-link", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, map[string]any{"links": s.usb.list()})
	})
	mux.HandleFunc("POST /pcsvr/usb-link/setup", func(w http.ResponseWriter, r *http.Request) {
		link, err := s.usb.setupWithAdmin(r.URL.Query().Get("name"))
		if err != nil {
			w.WriteHeader(http.StatusBadRequest)
		}
		writeJSON(w, link)
	})
	mux.HandleFunc("GET /pcsvr/api-names", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, s.apiNames.status())
	})
	mux.HandleFunc("POST /pcsvr/api-names/setup", func(w http.ResponseWriter, r *http.Request) {
		if err := s.apiNames.setupWithAdmin(s.targetIP()); err != nil {
			w.WriteHeader(http.StatusBadRequest)
			writeJSON(w, map[string]any{"state": "failed", "error": err.Error()})
			return
		}
		writeJSON(w, map[string]any{"state": "configured"})
	})
	mux.HandleFunc("GET /pcsvr/version", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, map[string]string{
			"version":    versionString(),
			"build_time": "",
			"git_commit": "linux-port",
			"go_version": "go",
			"platform":   "linux/amd64",
			"status":     "running",
		})
	})
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	})
	mux.HandleFunc("GET /devices_list/ws", s.devicesWS)
	mux.HandleFunc("GET /pcsvr/status", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, s.statusSnapshot())
	})
	mux.HandleFunc("POST /pcsvr/select_device", s.selectDevice)
	mux.HandleFunc("POST /select_device", s.selectDevice)
	mux.HandleFunc("POST /account/update_auth_key", s.updateAuthKey)
	mux.HandleFunc("POST /pcsvr/account/update_auth_key", s.updateAuthKey)
	registerCompat(mux)
	registerService(mux, s)
	mux.HandleFunc("GET /pcsvr/dns/records", s.listDNS)
}

func (s *state) updateAuthKey(w http.ResponseWriter, r *http.Request) {
	var body struct {
		DeviceID string `json:"device_id"`
		AuthKey  string `json:"auth_key"`
	}
	raw, err := io.ReadAll(io.LimitReader(r.Body, maxBody))
	if err != nil {
		s.noteEvent("update_auth_key_rejected", "read")
		writeJSON(w, map[string]any{"status": "error", "error_code": 40001, "msg": "type mismatch for field \"body\""})
		return
	}
	if len(strings.TrimSpace(string(raw))) == 0 {
		s.noteEvent("update_auth_key_rejected", "empty")
		writeJSON(w, map[string]any{"status": "error", "error_code": 40001, "msg": "Rejected auth key update because request is empty"})
		return
	}
	if err := json.Unmarshal(raw, &body); err != nil {
		s.noteEvent("update_auth_key_rejected", "json")
		writeJSON(w, map[string]any{"status": "error", "error_code": 40001, "msg": "type mismatch for field \"body\""})
		return
	}
	if strings.TrimSpace(body.DeviceID) == "" || strings.TrimSpace(body.AuthKey) == "" {
		s.noteEvent("update_auth_key_rejected", "missing")
		writeJSON(w, map[string]any{"status": "error", "error_code": 40001, "msg": "Rejected auth key update because device_id or auth_key is missing"})
		return
	}
	// UpdateAuthKey compares the key length with 10 and rejects the
	// shorter keys with error 40001.
	if len(body.AuthKey) < 10 {
		s.noteEvent("update_auth_key_rejected", "short")
		writeJSON(w, map[string]any{"status": "error", "error_code": 40001, "msg": "Rejected auth key update because the auth key is too short"})
		return
	}
	// The predicate at 0x6bec30 returns true when the auth key length is
	// not zero. The false path returns 40002 with the 35-byte sentence
	// and logs the 53-byte expired sentence.
	if !authKeyPresent(body.AuthKey) {
		s.noteEvent("update_auth_key_rejected", "Rejected auth key update because the auth key expired")
		writeJSON(w, map[string]any{"status": "error", "error_code": 40002, "msg": "auth key time out, please try again"})
		return
	}
	s.mu.Lock()
	s.authKeys[body.DeviceID] = body.AuthKey
	s.mu.Unlock()
	s.rememberAuth(body.DeviceID, body.AuthKey)
	expire := time.Now().UTC().Add(24 * time.Hour)
	if err := saveAuthFile(body.DeviceID, body.AuthKey, expire); err != nil {
		// The file writer's error is logged. The log call at 0x6bcab4
		// stores level "error", category "auth", the 22-byte event
		// "update_auth_key_failed", and the 26-byte message
		// "Failed to persist auth key". The HTTP body is the separate
		// 45-byte sentence with code 50001.
		s.noteEvent("update_auth_key_failed", "Failed to persist auth key")
		writeJSON(w, map[string]any{"status": "error", "error_code": 50001, "msg": "Failed to save auth key to persistent storage"})
		return
	}
	// After the write, UpdateAuthKey reloads the file. A load error
	// falls through to the writer at 0x6bce18: status "error", code
	// 50001, and the 37-byte message. The log just before it is level
	// "error", category "auth", event "update_auth_key_failed", and the
	// 35-byte message "Failed to verify persisted auth key".
	saved, ok := loadAuthFile(body.DeviceID)
	if !ok {
		s.noteEvent("update_auth_key_failed", "Failed to verify persisted auth key")
		writeJSON(w, map[string]any{"status": "error", "error_code": 50001, "msg": "Failed to verify auth key persistence"})
		return
	}
	// compareAuthInfo at 0x6bd3b0 returns false when the saved record
	// differs. That branch logs level "warn", category "auth", event
	// "storage_inconsistency", and "Detected auth key storage
	// inconsistency", then joins the success path.
	if saved.AuthKey != body.AuthKey || !saved.ExpireTime.Equal(expire) {
		s.noteEvent("storage_inconsistency", "Detected auth key storage inconsistency")
	}
	// The success log at 0x6bd18c stores level "info" (0x73b615, 4
	// bytes), category "auth" (0x73b871, 4), event "auth_key_updated"
	// (0x746bb8, 16), and the 27-byte message "Updated auth key for device".
	s.noteEvent("auth_key_updated", "Updated auth key for device")
	writeJSON(w, map[string]any{"status": "ok", "error_code": 0, "msg": "Updated auth key for device"})
}

// saveAuthFile writes auth_data/<device id>.json. The directory name is
// the 9-byte slice "auth_data" and the file name is "%s.json". A missing
// directory is created. The returned error is the writer's own text; the
// HTTP handler replaces it with the 45-byte sentence.
// authKeyPresent is the four-instruction predicate at 0x6bec30:
// compare the length with zero and return true when it is not zero.
func authKeyPresent(authKey string) bool {
	return len(authKey) != 0
}

// authFileInfo is the svc.AuthInfo record. The two fields recovered
// from the type are AuthKey (json "auth_key") and ExpireTime (json
// "expire_time", a time.Time). The file name also carries the device id.
type authFileInfo struct {
	AuthKey    string    `json:"auth_key"`
	ExpireTime time.Time `json:"expire_time"`
}

func saveAuthFile(deviceID, authKey string, expire time.Time) error {
	dir := authDir()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("failed to create auth_data directory: %v", err)
	}
	name := fmt.Sprintf("%s.json", deviceID)
	body, err := json.Marshal(authFileInfo{AuthKey: authKey, ExpireTime: expire.UTC()})
	if err != nil {
		return fmt.Errorf("failed to marshal auth info: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, name), body, 0o600); err != nil {
		return fmt.Errorf("failed to write auth file: %v", err)
	}
	return nil
}

// removeAuthFile deletes auth_data/<device id>.json. It reports
// whether a file was removed. A missing file is not an error.
func removeAuthFile(deviceID string) bool {
	path := filepath.Join(authDir(), fmt.Sprintf("%s.json", deviceID))
	err := os.Remove(path)
	return err == nil
}

func authDir() string {
	dir := os.Getenv("PCSVR_AUTH_DIR")
	if dir == "" {
		return "auth_data"
	}
	return dir
}

// loadAuthFile reads auth_data/<device id>.json. The loader at 0x6b2a80
// returns an error for a missing file ("auth file not found for device")
// and for JSON that does not unmarshal ("failed to unmarshal auth info").
// A missing or unreadable file is the path that continues to the
// missing-key HTTP response.
func loadAuthFile(deviceID string) (authFileInfo, bool) {
	raw, err := os.ReadFile(filepath.Join(authDir(), fmt.Sprintf("%s.json", deviceID)))
	if err != nil {
		return authFileInfo{}, false
	}
	var info authFileInfo
	if err := json.Unmarshal(raw, &info); err != nil {
		return authFileInfo{}, false
	}
	if info.AuthKey == "" {
		return authFileInfo{}, false
	}
	return info, true
}

func (s *state) selectDevice(w http.ResponseWriter, r *http.Request) {
	var body struct {
		DeviceID string `json:"device_id"`
		Local    *bool  `json:"local"`
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, maxBody)).Decode(&body); err != nil && !errors.Is(err, io.EOF) {
		s.noteEvent("select_failed", "json")
		writeJSON(w, map[string]any{"status": "error", "error_code": 40001, "msg": "type mismatch for field \"body\""})
		return
	}
	s.mu.Lock()
	known := false
	if body.DeviceID == "" {
		s.selected = ""
		known = true
	} else if _, ok := s.found[body.DeviceID]; ok {
		s.selected = body.DeviceID
		known = true
	}
	s.mu.Unlock()
	if !known {
		// SelectDevice stores error 40440. The message is the 38-byte
		// prefix "local device not found for device_id: " plus the id.
		s.noteEvent("select_failed", body.DeviceID)
		writeJSON(w, map[string]any{
			"status":     "error",
			"error_code": 40440,
			"msg":        "local device not found for device_id: " + body.DeviceID,
		})
		return
	}
	s.publishChange()
	local := false
	var selectedID, deviceIP, deviceName string
	snap := s.current()
	if snap.Data.SelectedDevice != nil {
		local = snap.Data.SelectedDevice.Local
		selectedID = snap.Data.SelectedDevice.DeviceID
		deviceIP = snap.Data.SelectedDevice.DeviceIP
		deviceName = snap.Data.SelectedDevice.DeviceName
	}
	if body.Local != nil {
		local = *body.Local
	}
	// The UI treats any status other than "ok" as a failed select and
	// logs msg. An empty device id is the clear path: the 49-byte
	// sentence "cleared selected device and restored default page".
	// SelectDeviceResp fields are status, error_code,
	// selected_device_id, device_ip, device_name, local,
	// auto_selected, user_info, and msg.
	msg := "Selected discovered device for default DNS resolution"
	if body.DeviceID == "" {
		msg = "cleared selected device and restored default page"
		selectedID, deviceIP, deviceName = "", "", ""
	}
	writeJSON(w, map[string]any{
		"status":             "ok",
		"error_code":         0,
		"selected_device_id": selectedID,
		"device_ip":          deviceIP,
		"device_name":        deviceName,
		"local":              local,
		"auto_selected":      false,
		"user_info":          nil,
		"msg":                msg,
	})
}

func (s *state) devicesWS(w http.ResponseWriter, r *http.Request) {
	hj, ok := w.(http.Hijacker)
	if !ok {
		http.Error(w, "hijack unsupported", http.StatusInternalServerError)
		return
	}
	conn, rw, err := hj.Hijack()
	if err != nil {
		return
	}
	defer conn.Close()

	key := websocketAccept(r.Header.Get("Sec-WebSocket-Key"))
	if key == "" {
		_, _ = rw.WriteString("HTTP/1.1 400 Bad Request\r\nContent-Length: 0\r\nConnection: close\r\n\r\n")
		_ = rw.Flush()
		return
	}
	_, _ = rw.WriteString("HTTP/1.1 101 Switching Protocols\r\nUpgrade: websocket\r\nConnection: Upgrade\r\nSec-WebSocket-Accept: " + key + "\r\n\r\n")
	if err := rw.Flush(); err != nil {
		return
	}

	ch, cancel := s.subscribe()
	defer cancel()
	// The first frame is the connection snapshot. Later frames are
	// the diff events, each one {event, data}. A publish that landed
	// between subscribe and this snapshot already moved wsPrev, so
	// this write must not roll that baseline back.
	s.mu.Lock()
	first := s.currentLocked()
	if len(s.clients) == 1 && len(deviceItems(s.wsPrev)) == 0 && s.wsPrev.Data.SelectedDevice == nil {
		s.wsPrev = first
	}
	s.mu.Unlock()
	heartbeats := make(chan struct{}, 1)
	out := make(chan []byte, 32)
	out <- mustJSON(first)
	go func() {
		for payload := range out {
			if err := writeWSText(conn, payload); err != nil {
				return
			}
		}
	}()
	// Client text is read on its own goroutine. The binary compares
	// that text with the 9-byte word "heartbeat".
	go func() {
		for {
			_ = conn.SetReadDeadline(time.Now().Add(90 * time.Second))
			opcode, payload, err := readWS(conn)
			if err != nil {
				return
			}
			if opcode == 0x1 && string(payload) == "heartbeat" {
				select {
				case heartbeats <- struct{}{}:
				default:
				}
			}
		}
	}()
	ping := time.NewTicker(25 * time.Second)
	defer ping.Stop()
	for {
		select {
		case events := <-ch:
			for _, event := range events {
				select {
				case out <- mustJSON(event):
				default:
				}
			}
		case <-heartbeats:
			select {
			case out <- mustJSON(heartbeatPayload()):
			default:
			}
		case <-ping.C:
			select {
			case out <- mustJSON(heartbeatPayload()):
			default:
			}
		}
	}
}

// heartbeatPayload is devicesListWSHeartbeatPayload. The map field is
// the 9-byte word "timestamp" and the value is time.RFC3339. The event
// name is supplied by the caller.
func heartbeatPayload() map[string]string {
	return map[string]string{
		"event":     "heartbeat",
		"timestamp": time.Now().UTC().Format(time.RFC3339),
	}
}

func (s *state) dnsRecords() []map[string]any {
	ip := s.targetIP()
	if ip == "" {
		ip = "127.0.0.1"
	}
	names := []string{
		s.cfg.Domain,
		"api." + s.cfg.Domain,
		"wifi.api." + s.cfg.Domain,
		"kb.api." + s.cfg.Domain,
		"chat-history.api." + s.cfg.Domain,
		"auth.api." + s.cfg.Domain,
		"p8800.api." + s.cfg.Domain,
		"tts.api." + s.cfg.Domain,
		"connector.api." + s.cfg.Domain,
		"agent-services.api." + s.cfg.Domain,
		"backup.managed." + s.cfg.Domain,
	}
	out := make([]map[string]any, 0, len(names))
	for _, name := range names {
		out = append(out, map[string]any{"fqdn": name, "ipv4": ip, "ttl": dnsTTL})
	}
	svc := s.service()
	svc.mu.Lock()
	out = append(out, svc.records...)
	svc.mu.Unlock()
	return out
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	enc := json.NewEncoder(w)
	enc.SetEscapeHTML(false)
	_ = enc.Encode(v)
}

func mustJSON(v any) []byte {
	b, err := json.Marshal(v)
	if err != nil {
		return []byte(`{"event":"connection","data":{}}`)
	}
	return b
}

// listenRetry retries a bind that fails with EADDRINUSE for up to
// bindRetryFor. 60000, 60022 and 60053 are fixed by the app and lie in
// the default Linux ephemeral range (32768-60999), so an outgoing
// loopback connection can hold one as its local port for a while.
// Reserving them with net.ipv4.ip_local_reserved_ports avoids the wait.
func listenRetry[T any](listen func() (T, error)) (T, error) {
	deadline := time.Now().Add(bindRetryFor)
	logged := false
	for {
		v, err := listen()
		if err == nil || !errors.Is(err, syscall.EADDRINUSE) || time.Now().After(deadline) {
			return v, err
		}
		if !logged {
			log.Printf("%v; retrying for %s", err, bindRetryFor)
			logged = true
		}
		time.Sleep(bindRetryEvery)
	}
}
