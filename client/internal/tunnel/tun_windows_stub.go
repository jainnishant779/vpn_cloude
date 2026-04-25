//go:build !windows
// +build !windows

package tunnel

// WindowsTUNDevice is a stub for non-Windows platforms.
// Type assertions like device.(*WindowsTUNDevice) will return ok=false.
type WindowsTUNDevice struct{}

func (d *WindowsTUNDevice) SetupWireGuard(privateKey string, listenPort int) error { return nil }
func (d *WindowsTUNDevice) AddWGPeer(publicKey, endpoint, allowedIP string) error  { return nil }
func (d *WindowsTUNDevice) RemoveWGPeer(publicKey string) error                    { return nil }
func (d *WindowsTUNDevice) Name() string                                           { return "" }
func (d *WindowsTUNDevice) Read(buf []byte) (int, error)                           { return 0, nil }
func (d *WindowsTUNDevice) Write(buf []byte) (int, error)                          { return 0, nil }
func (d *WindowsTUNDevice) Close() error                                           { return nil }
func (d *WindowsTUNDevice) Configure(ip, cidr string) error                        { return nil }
