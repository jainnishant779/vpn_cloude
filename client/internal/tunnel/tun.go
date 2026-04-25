package tunnel

// TUNDevice is the common interface for all platforms.
type TUNDevice interface {
	Name() string
	Configure(virtualIP, cidr string) error
	Read(buf []byte) (int, error)
	Write(buf []byte) (int, error)
	Close() error
}
