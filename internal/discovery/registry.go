package discovery

import (
	"context"
	"fmt"
	"log/slog"
	"net"
	"sort"
	"sync"
	"time"

	"github.com/vishen/go-chromecast/dns"
)

type Device struct {
	Entry    dns.CastEntry
	LastSeen time.Time
}

type DeviceView struct {
	AddrV4     string            `json:"addrV4"`
	AddrV6     string            `json:"addrV6"`
	Port       int               `json:"port"`
	Name       string            `json:"name"`
	Host       string            `json:"host"`
	UUID       string            `json:"uuid"`
	Device     string            `json:"device"`
	Status     string            `json:"status"`
	DeviceName string            `json:"deviceName"`
	InfoFields map[string]string `json:"infoFields"`
	LastSeen   time.Time         `json:"lastSeen"`
}

func (d Device) View() DeviceView {
	infoFields := make(map[string]string, len(d.Entry.InfoFields))
	for key, value := range d.Entry.InfoFields {
		infoFields[key] = value
	}
	return DeviceView{
		AddrV4:     ipString(d.Entry.AddrV4),
		AddrV6:     ipString(d.Entry.AddrV6),
		Port:       d.Entry.Port,
		Name:       d.Entry.Name,
		Host:       d.Entry.Host,
		UUID:       d.Entry.UUID,
		Device:     d.Entry.Device,
		Status:     d.Entry.Status,
		DeviceName: d.Entry.DeviceName,
		InfoFields: infoFields,
		LastSeen:   d.LastSeen,
	}
}

func ipString(ip net.IP) string {
	if ip == nil {
		return ""
	}
	return ip.String()
}

type Registry struct {
	mu      sync.RWMutex
	devices map[string]Device
	ttl     time.Duration
}

func NewRegistry(ttl time.Duration) *Registry {
	return &Registry{devices: make(map[string]Device), ttl: ttl}
}

func (r *Registry) Start(ctx context.Context, interfaceName string, logger *slog.Logger) error {
	var networkInterface *net.Interface
	if interfaceName != "" {
		var err error
		networkInterface, err = net.InterfaceByName(interfaceName)
		if err != nil {
			return fmt.Errorf("find discovery interface %q: %w", interfaceName, err)
		}
	}

	entries, err := dns.DiscoverCastDNSEntries(ctx, networkInterface)
	if err != nil {
		return fmt.Errorf("start mDNS discovery: %w", err)
	}

	go r.consume(ctx, entries, logger)
	return nil
}

func (r *Registry) consume(ctx context.Context, entries <-chan dns.CastEntry, logger *slog.Logger) {
	for {
		select {
		case <-ctx.Done():
			return
		case entry, ok := <-entries:
			if !ok {
				return
			}
			if entry.UUID == "" {
				logger.Debug("ignoring Cast announcement without UUID", "host", entry.Host)
				continue
			}
			now := time.Now().UTC()
			r.mu.Lock()
			previous, existed := r.devices[entry.UUID]
			isNew := !existed || !previous.LastSeen.After(now.Add(-r.ttl))
			r.devices[entry.UUID] = Device{Entry: entry, LastSeen: now}
			r.mu.Unlock()
			message := "received Cast mDNS announcement"
			if isNew {
				message = "discovered Cast device"
			}
			logger.Info(message,
				"uuid", entry.UUID,
				"name", entry.DeviceName,
				"model", entry.Device,
				"address", entry.GetAddr(),
				"port", entry.Port,
			)
		}
	}
}

func (r *Registry) Get(id string) (Device, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	device, ok := r.devices[id]
	if ok && !device.LastSeen.After(time.Now().Add(-r.ttl)) {
		delete(r.devices, id)
		return Device{}, false
	}
	return device, ok
}

func (r *Registry) Snapshot() []Device {
	cutoff := time.Now().Add(-r.ttl)
	r.mu.Lock()
	devices := make([]Device, 0, len(r.devices))
	for id, device := range r.devices {
		if !device.LastSeen.After(cutoff) {
			delete(r.devices, id)
			continue
		}
		devices = append(devices, device)
	}
	r.mu.Unlock()

	sort.Slice(devices, func(i, j int) bool {
		return devices[i].Entry.UUID < devices[j].Entry.UUID
	})
	return devices
}
