package ramune

import (
	"encoding/json"
	"fmt"
	"net"
)

// goDNSLookup performs a DNS lookup for the given hostname and returns the
// first address as a JSON object with "address" and "family" fields.
func goDNSLookup(args []any) (any, error) {
	if len(args) < 1 {
		return nil, fmt.Errorf("dns.lookup: hostname required")
	}
	hostname, _ := args[0].(string)
	addrs, err := net.LookupHost(hostname)
	if err != nil {
		return nil, err
	}
	if len(addrs) == 0 {
		return nil, fmt.Errorf("dns: no addresses for %s", hostname)
	}
	// Return the first address (like Node.js dns.lookup).
	family := 4
	if net.ParseIP(addrs[0]).To4() == nil {
		family = 6
	}
	result := map[string]any{
		"address": addrs[0],
		"family":  family,
	}
	out, _ := json.Marshal(result)
	return string(out), nil
}

// goDNSResolve performs a DNS lookup and returns all addresses as a JSON array.
func goDNSResolve(args []any) (any, error) {
	if len(args) < 1 {
		return nil, fmt.Errorf("dns.resolve: hostname required")
	}
	hostname, _ := args[0].(string)
	addrs, err := net.LookupHost(hostname)
	if err != nil {
		return nil, err
	}
	out, _ := json.Marshal(addrs)
	return string(out), nil
}

// installDNS registers DNS callbacks and the dns JS module.
// Must be called with rt.mu held.
func (r *Runtime) installDNS() error {
	if err := r.registerFuncLocked("__go_dns_lookup", goDNSLookup); err != nil {
		return err
	}
	if err := r.registerFuncLocked("__go_dns_resolve", goDNSResolve); err != nil {
		return err
	}
	return r.execLocked(dnsJSSource())
}

func dnsJSSource() string {
	return `(function() {
	var dns = {
		lookup: function(hostname, opts, cb) {
			if (typeof opts === 'function') { cb = opts; }
			try {
				var result = JSON.parse(__go_dns_lookup(hostname));
				if (cb) setTimeout(function() { cb(null, result.address, result.family); }, 0);
			} catch(e) {
				if (cb) setTimeout(function() { cb(e); }, 0);
			}
		},
		resolve: function(hostname, cb) {
			try {
				var addrs = JSON.parse(__go_dns_resolve(hostname));
				if (cb) setTimeout(function() { cb(null, addrs); }, 0);
			} catch(e) {
				if (cb) setTimeout(function() { cb(e); }, 0);
			}
		},
		resolve4: function(hostname, cb) { dns.resolve(hostname, cb); },
		promises: {
			lookup: function(hostname) {
				return new Promise(function(resolve, reject) {
					try {
						var r = JSON.parse(__go_dns_lookup(hostname));
						resolve(r);
					} catch(e) { reject(e); }
				});
			},
			resolve: function(hostname) {
				return new Promise(function(resolve, reject) {
					try { resolve(JSON.parse(__go_dns_resolve(hostname))); }
					catch(e) { reject(e); }
				});
			}
		}
	};
	if (typeof globalThis.require !== 'undefined' && globalThis.require._modules) {
		globalThis.require._modules['dns'] = dns;
	}
})();
`
}
