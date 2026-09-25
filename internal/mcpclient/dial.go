package mcpclient

import (
	"context"
	"net"
	"net/netip"
	"sort"
	"strings"
	"time"

	"local-agent-workbench/internal/egress"
)

// sharedAddressSpace — 100.64.0.0/10. Формально не частная сеть, но её
// занимают VPN (Tailscale и подобные) — ровно там живёт GitLab «во
// внутренней сети» у многих команд.
var sharedAddressSpace = netip.MustParsePrefix("100.64.0.0/10")

// grantable — адрес, который владелец может разрешить конкретному узлу:
// частная сеть, loopback, общее пространство VPN. Link-local (в том числе
// metadata облака 169.254.169.254), multicast и unspecified не разрешаются
// никогда: туда удалённому серверу MCP ходить незачем.
func grantable(address netip.Addr) bool {
	return address.IsPrivate() || address.IsLoopback() || sharedAddressSpace.Contains(address)
}

// PinAddress выбирает адрес, на который пойдёт соединение, и проверяет его.
//
// Публичный узел проверяется тем же правилом, что и сеть агентов
// (egress.ResolvePinned): все ответы DNS публичные — соединение идёт на
// закреплённый адрес, повторного разрешения нет. Узел во внутренней сети
// пропускается, только если владелец разрешил именно его (granted) и все
// ответы DNS — разрешимые частные адреса. Смесь публичного и частного в
// одном ответе похожа на подмену DNS и отклоняется.
func PinAddress(ctx context.Context, resolver egress.Resolver, host, granted string) (netip.Addr, error) {
	if resolver == nil {
		resolver = net.DefaultResolver
	}
	name := normalizeHost(host)
	allowed := granted != "" && name == normalizeHost(granted)
	if literal, err := netip.ParseAddr(name); err == nil {
		literal = literal.Unmap()
		switch {
		case grantable(literal) && allowed:
			return literal, nil
		case grantable(literal):
			return netip.Addr{}, newError(KindPrivateHostDenied, nil, "%s is an address in an internal network and is not allowed for this server", name)
		default:
			return netip.Addr{}, newError(KindNetwork, nil, "server address %s is given as a number; use the host name", name)
		}
	}
	if ip, err := egress.ResolvePinned(ctx, resolver, name); err == nil {
		if address, ok := netip.AddrFromSlice(ip); ok {
			return address.Unmap(), nil
		}
	}
	answers, err := resolver.LookupIPAddr(ctx, name)
	if err != nil {
		return netip.Addr{}, newError(KindNetwork, err, "cannot resolve %s", name)
	}
	if len(answers) == 0 {
		return netip.Addr{}, newError(KindNetwork, nil, "%s has no addresses", name)
	}
	addresses := make([]netip.Addr, 0, len(answers))
	for _, answer := range answers {
		address, ok := netip.AddrFromSlice(answer.IP)
		if !ok {
			return netip.Addr{}, newError(KindNetwork, nil, "%s resolved to an unreadable address", name)
		}
		address = address.Unmap()
		if !grantable(address) {
			return netip.Addr{}, newError(KindNetwork, nil, "%s resolves to %s, which is never allowed for a remote server", name, address)
		}
		addresses = append(addresses, address)
	}
	sort.Slice(addresses, func(i, j int) bool { return addresses[i].Less(addresses[j]) })
	if !allowed {
		return netip.Addr{}, newError(KindPrivateHostDenied, nil, "%s is in an internal network (%s) and is not allowed for this server", name, addresses[0])
	}
	return addresses[0], nil
}

// PinnedDialer — DialContext удалённого сервера: адрес проверяется при
// каждом соединении и закрепляется. Имя узла остаётся в TLS (SNI и проверка
// сертификата), потому что http.Transport берёт его из URL, а не из адреса.
func PinnedDialer(resolver egress.Resolver, granted string) func(ctx context.Context, network, address string) (net.Conn, error) {
	dialer := &net.Dialer{Timeout: 10 * time.Second}
	return func(ctx context.Context, network, address string) (net.Conn, error) {
		host, port, err := net.SplitHostPort(address)
		if err != nil {
			return nil, newError(KindNetwork, err, "bad address %q", address)
		}
		pinned, err := PinAddress(ctx, resolver, host, granted)
		if err != nil {
			return nil, err
		}
		conn, err := dialer.DialContext(ctx, network, net.JoinHostPort(pinned.String(), port))
		if err != nil {
			return nil, newError(KindNetwork, err, "cannot connect to %s", host)
		}
		return conn, nil
	}
}

func normalizeHost(host string) string {
	host = strings.TrimSpace(host)
	host = strings.TrimPrefix(strings.TrimSuffix(host, "]"), "[")
	return strings.ToLower(strings.TrimSuffix(host, "."))
}
