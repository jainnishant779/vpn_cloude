//go:build windows
// +build windows

package tunnel

import (
	"fmt"
	"os/exec"
	"strconv"
	"strings"

	"golang.zx2c4.com/wireguard/conn"
	"golang.zx2c4.com/wireguard/device"
	"golang.zx2c4.com/wireguard/ipc"
	"golang.zx2c4.com/wireguard/tun"
)

type WindowsTUNDevice struct {
	dev     tun.Device
	wgDev   *device.Device
	uapiSrv *ipc.UAPIListener
	name    string
}

func (d *WindowsTUNDevice) Create(name string, mtu int) error {
	if mtu <= 0 {
		mtu = 1420
	}
	tunDev, err := tun.CreateTUN(name, mtu)
	if err != nil {
		return fmt.Errorf("windows tun create: %w", err)
	}
	resolvedName, err := tunDev.Name()
	if err != nil {
		_ = tunDev.Close()
		return fmt.Errorf("windows tun name: %w", err)
	}
	d.dev  = tunDev
	d.name = resolvedName
	return nil
}

func (d *WindowsTUNDevice) Configure(ip string, cidr string) error {
	if d.name == "" {
		return fmt.Errorf("windows tun configure: not created")
	}
	return ConfigureTUN(d.name, ip, cidr)
}

func (d *WindowsTUNDevice) SetupWireGuard(privateKey string, listenPort int) error {
	if d.dev == nil {
		return fmt.Errorf("tun device not created")
	}
	logger := device.NewLogger(device.LogLevelError, "[quicktunnel] ")
	wgDev := device.NewDevice(d.dev, conn.NewDefaultBind(), logger)

	// Configure via IPC (same protocol as wg set)
	ipcConf := fmt.Sprintf("private_key=%s\nlisten_port=%d\n",
		keyToHex(privateKey), listenPort)

	if err := wgDev.IpcSet(ipcConf); err != nil {
		wgDev.Close()
		return fmt.Errorf("wg ipc set: %w", err)
	}

	if err := wgDev.Up(); err != nil {
		wgDev.Close()
		return fmt.Errorf("wg device up: %w", err)
	}

	d.wgDev = wgDev

	// Start UAPI listener so wg.exe can also communicate
	uapi, err := ipc.UAPIListen(d.name)
	if err == nil {
		d.uapiSrv = uapi
		go func() {
			for {
				c, err := uapi.Accept()
				if err != nil {
					return
				}
				go wgDev.IpcHandle(c)
			}
		}()
	}
	return nil
}

func (d *WindowsTUNDevice) AddWGPeer(publicKey, endpoint, allowedIP string) error {
	if d.wgDev == nil {
		return fmt.Errorf("wireguard not initialized")
	}
	if !strings.Contains(allowedIP, "/") {
		allowedIP += "/32"
	}
	conf := fmt.Sprintf("public_key=%s\nendpoint=%s\nallowed_ip=%s\npersistent_keepalive_interval=25\n",
		keyToHex(publicKey), endpoint, allowedIP)
	return d.wgDev.IpcSet(conf)
}

func (d *WindowsTUNDevice) Read(buf []byte) (int, error) {
	if d.dev == nil {
		return 0, fmt.Errorf("device not created")
	}
	packets := [][]byte{buf}
	sizes   := make([]int, 1)
	_, err  := d.dev.Read(packets, sizes, 0)
	if err != nil {
		return 0, err
	}
	return sizes[0], nil
}

func (d *WindowsTUNDevice) Write(buf []byte) (int, error) {
	if d.dev == nil {
		return 0, fmt.Errorf("device not created")
	}
	_, err := d.dev.Write([][]byte{buf}, 0)
	return len(buf), err
}

func (d *WindowsTUNDevice) Close() error {
	if d.uapiSrv != nil {
		_ = d.uapiSrv.Close()
	}
	if d.wgDev != nil {
		d.wgDev.Close()
	}
	if d.dev != nil {
		return d.dev.Close()
	}
	return nil
}

func (d *WindowsTUNDevice) Name() string { return d.name }

func CreateTUN(name string, mtu int) (TUNDevice, error) {
	dev := &WindowsTUNDevice{}
	if err := dev.Create(name, mtu); err != nil {
		return nil, fmt.Errorf("create tun: %w", err)
	}
	return dev, nil
}

func ConfigureTUN(name string, ip string, cidr string) error {
	mask, err := maskStringFromCIDR(cidr)
	if err != nil {
		return fmt.Errorf("configure tun: derive mask: %w", err)
	}
	if err := runNetsh("interface", "ip", "set", "address",
		"name="+name, "source=static", "addr="+ip, "mask="+mask); err != nil {
		return fmt.Errorf("configure tun: set ip: %w", err)
	}
	return nil
}

func SetMTU(name string, mtu int) error {
	return runNetsh("interface", "ipv4", "set", "subinterface",
		name, "mtu="+strconv.Itoa(mtu), "store=active")
}

func DestroyTUN(name string) error {
	_ = runNetsh("interface", "set", "interface", "name="+name, "admin=disabled")
	return nil
}

func runNetsh(args ...string) error {
	out, err := exec.Command("netsh", args...).CombinedOutput()
	if err != nil {
		return fmt.Errorf("netsh %v: %w (out: %s)", args, err, strings.TrimSpace(string(out)))
	}
	return nil
}

// keyToHex converts a base64 WireGuard key to lowercase hex (IPC format).
func keyToHex(b64key string) string {
	import_b64 := strings.TrimSpace(b64key)
	_ = import_b64
	// Use wireguard key parsing
	var key device.NoisePrivateKey
	if err := key.FromMaybeZeroHex(b64key); err == nil {
		return fmt.Sprintf("%x", key)
	}
	return b64key
}
