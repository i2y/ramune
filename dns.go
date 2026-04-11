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

func goDNSResolve4(args []any) (any, error) {
	if len(args) < 1 {
		return nil, fmt.Errorf("dns.resolve4: hostname required")
	}
	hostname, _ := args[0].(string)
	ips, err := net.LookupIP(hostname)
	if err != nil {
		return nil, err
	}
	var v4 []string
	for _, ip := range ips {
		if ip4 := ip.To4(); ip4 != nil {
			v4 = append(v4, ip4.String())
		}
	}
	out, _ := json.Marshal(v4)
	return string(out), nil
}

func goDNSResolve6(args []any) (any, error) {
	if len(args) < 1 {
		return nil, fmt.Errorf("dns.resolve6: hostname required")
	}
	hostname, _ := args[0].(string)
	ips, err := net.LookupIP(hostname)
	if err != nil {
		return nil, err
	}
	var v6 []string
	for _, ip := range ips {
		if ip.To4() == nil {
			v6 = append(v6, ip.String())
		}
	}
	out, _ := json.Marshal(v6)
	return string(out), nil
}

func goDNSResolveMx(args []any) (any, error) {
	if len(args) < 1 {
		return nil, fmt.Errorf("dns.resolveMx: hostname required")
	}
	hostname, _ := args[0].(string)
	mxs, err := net.LookupMX(hostname)
	if err != nil {
		return nil, err
	}
	var result []map[string]any
	for _, mx := range mxs {
		result = append(result, map[string]any{
			"exchange": mx.Host,
			"priority": mx.Pref,
		})
	}
	out, _ := json.Marshal(result)
	return string(out), nil
}

func goDNSResolveTxt(args []any) (any, error) {
	if len(args) < 1 {
		return nil, fmt.Errorf("dns.resolveTxt: hostname required")
	}
	hostname, _ := args[0].(string)
	records, err := net.LookupTXT(hostname)
	if err != nil {
		return nil, err
	}
	// Node.js returns array of arrays of strings.
	var result [][]string
	for _, r := range records {
		result = append(result, []string{r})
	}
	out, _ := json.Marshal(result)
	return string(out), nil
}

func goDNSResolveCname(args []any) (any, error) {
	if len(args) < 1 {
		return nil, fmt.Errorf("dns.resolveCname: hostname required")
	}
	hostname, _ := args[0].(string)
	cname, err := net.LookupCNAME(hostname)
	if err != nil {
		return nil, err
	}
	out, _ := json.Marshal([]string{cname})
	return string(out), nil
}

func goDNSResolveNs(args []any) (any, error) {
	if len(args) < 1 {
		return nil, fmt.Errorf("dns.resolveNs: hostname required")
	}
	hostname, _ := args[0].(string)
	nss, err := net.LookupNS(hostname)
	if err != nil {
		return nil, err
	}
	var result []string
	for _, ns := range nss {
		result = append(result, ns.Host)
	}
	out, _ := json.Marshal(result)
	return string(out), nil
}

func goDNSResolveSrv(args []any) (any, error) {
	if len(args) < 1 {
		return nil, fmt.Errorf("dns.resolveSrv: hostname required")
	}
	hostname, _ := args[0].(string)
	_, srvs, err := net.LookupSRV("", "", hostname)
	if err != nil {
		return nil, err
	}
	var result []map[string]any
	for _, srv := range srvs {
		result = append(result, map[string]any{
			"name":     srv.Target,
			"port":     srv.Port,
			"priority": srv.Priority,
			"weight":   srv.Weight,
		})
	}
	out, _ := json.Marshal(result)
	return string(out), nil
}

func goDNSReverse(args []any) (any, error) {
	if len(args) < 1 {
		return nil, fmt.Errorf("dns.reverse: ip required")
	}
	ip, _ := args[0].(string)
	names, err := net.LookupAddr(ip)
	if err != nil {
		return nil, err
	}
	out, _ := json.Marshal(names)
	return string(out), nil
}

// installDNS registers DNS callbacks and the dns JS module.
// Must be called with rt.mu held.
func (r *Runtime) installDNS() error {
	funcs := map[string]func([]any) (any, error){
		"__go_dns_lookup":        goDNSLookup,
		"__go_dns_resolve":       goDNSResolve,
		"__go_dns_resolve4":      goDNSResolve4,
		"__go_dns_resolve6":      goDNSResolve6,
		"__go_dns_resolve_mx":    goDNSResolveMx,
		"__go_dns_resolve_txt":   goDNSResolveTxt,
		"__go_dns_resolve_cname": goDNSResolveCname,
		"__go_dns_resolve_ns":    goDNSResolveNs,
		"__go_dns_resolve_srv":   goDNSResolveSrv,
		"__go_dns_reverse":       goDNSReverse,
	}
	for name, fn := range funcs {
		if err := r.registerFuncLocked(name, fn); err != nil {
			return err
		}
	}
	return r.execLocked(dnsJSSource())
}

func dnsJSSource() string {
	return `(function() {
	function _dnsCallback(goFn, hostname, cb) {
		try {
			var result = JSON.parse(goFn(hostname));
			if (cb) setTimeout(function() { cb(null, result); }, 0);
		} catch(e) {
			if (cb) setTimeout(function() { cb(e); }, 0);
		}
	}
	function _dnsPromise(goFn, arg) {
		return new Promise(function(resolve, reject) {
			try { resolve(JSON.parse(goFn(arg))); }
			catch(e) { reject(e); }
		});
	}
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
		resolve: function(hostname, rrtype, cb) {
			if (typeof rrtype === 'function') { cb = rrtype; rrtype = 'A'; }
			var fns = { A: __go_dns_resolve4, AAAA: __go_dns_resolve6, MX: __go_dns_resolve_mx,
				TXT: __go_dns_resolve_txt, CNAME: __go_dns_resolve_cname, NS: __go_dns_resolve_ns,
				SRV: __go_dns_resolve_srv };
			var fn = fns[rrtype] || __go_dns_resolve;
			_dnsCallback(fn, hostname, cb);
		},
		resolve4: function(hostname, cb) { _dnsCallback(__go_dns_resolve4, hostname, cb); },
		resolve6: function(hostname, cb) { _dnsCallback(__go_dns_resolve6, hostname, cb); },
		resolveMx: function(hostname, cb) { _dnsCallback(__go_dns_resolve_mx, hostname, cb); },
		resolveTxt: function(hostname, cb) { _dnsCallback(__go_dns_resolve_txt, hostname, cb); },
		resolveCname: function(hostname, cb) { _dnsCallback(__go_dns_resolve_cname, hostname, cb); },
		resolveNs: function(hostname, cb) { _dnsCallback(__go_dns_resolve_ns, hostname, cb); },
		resolveSrv: function(hostname, cb) { _dnsCallback(__go_dns_resolve_srv, hostname, cb); },
		reverse: function(ip, cb) { _dnsCallback(__go_dns_reverse, ip, cb); },
		promises: {
			lookup: function(hostname) {
				return new Promise(function(resolve, reject) {
					try {
						var r = JSON.parse(__go_dns_lookup(hostname));
						resolve(r);
					} catch(e) { reject(e); }
				});
			},
			resolve: function(hostname, rrtype) {
				var fns = { A: __go_dns_resolve4, AAAA: __go_dns_resolve6, MX: __go_dns_resolve_mx,
					TXT: __go_dns_resolve_txt, CNAME: __go_dns_resolve_cname, NS: __go_dns_resolve_ns,
					SRV: __go_dns_resolve_srv };
				return _dnsPromise(fns[rrtype || 'A'] || __go_dns_resolve, hostname);
			},
			resolve4: function(hostname) { return _dnsPromise(__go_dns_resolve4, hostname); },
			resolve6: function(hostname) { return _dnsPromise(__go_dns_resolve6, hostname); },
			resolveMx: function(hostname) { return _dnsPromise(__go_dns_resolve_mx, hostname); },
			resolveTxt: function(hostname) { return _dnsPromise(__go_dns_resolve_txt, hostname); },
			resolveCname: function(hostname) { return _dnsPromise(__go_dns_resolve_cname, hostname); },
			resolveNs: function(hostname) { return _dnsPromise(__go_dns_resolve_ns, hostname); },
			resolveSrv: function(hostname) { return _dnsPromise(__go_dns_resolve_srv, hostname); },
			reverse: function(ip) { return _dnsPromise(__go_dns_reverse, ip); }
		}
	};
	if (typeof globalThis.require !== 'undefined' && globalThis.require._modules) {
		globalThis.require._modules['dns'] = dns;
	}
})();
`
}
