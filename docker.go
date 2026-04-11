package ramune

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"
)

var errInvalidDockerClient = fmt.Errorf("docker: invalid client")

// dockerClient wraps Docker Engine API calls over Unix socket or TCP.
type dockerClient struct {
	httpClient *http.Client
	baseURL    string
}

func newDockerClient(socketPath string) *dockerClient {
	if socketPath == "" {
		host := os.Getenv("DOCKER_HOST")
		if host != "" {
			if strings.HasPrefix(host, "unix://") {
				socketPath = strings.TrimPrefix(host, "unix://")
			} else if strings.HasPrefix(host, "tcp://") {
				return &dockerClient{
					httpClient: &http.Client{Timeout: 30 * time.Second},
					baseURL:    "http://" + strings.TrimPrefix(host, "tcp://"),
				}
			}
		}
		if socketPath == "" {
			socketPath = "/var/run/docker.sock"
		}
	}
	return &dockerClient{
		httpClient: &http.Client{
			Transport: &http.Transport{
				DialContext: func(_ context.Context, _, _ string) (net.Conn, error) {
					return net.Dial("unix", socketPath)
				},
			},
		},
		baseURL: "http://localhost",
	}
}

func (c *dockerClient) do(method, path string, body io.Reader) (*http.Response, error) {
	req, err := http.NewRequest(method, c.baseURL+path, body)
	if err != nil {
		return nil, err
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	return c.httpClient.Do(req)
}

func (c *dockerClient) doJSON(method, path string, body io.Reader) (map[string]any, error) {
	resp, err := c.do(method, path, body)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode >= 400 {
		return nil, fmt.Errorf("docker: %s %s: %s (status %d)", method, path, string(data), resp.StatusCode)
	}
	var result map[string]any
	if len(data) > 0 {
		if err := json.Unmarshal(data, &result); err != nil {
			return nil, fmt.Errorf("docker: invalid JSON: %v", err)
		}
	}
	return result, nil
}

func (c *dockerClient) ping() error {
	resp, err := c.do("GET", "/_ping", nil)
	if err != nil {
		return err
	}
	resp.Body.Close()
	if resp.StatusCode != 200 {
		return fmt.Errorf("docker ping: status %d", resp.StatusCode)
	}
	return nil
}

func (c *dockerClient) imageInspect(name string) (map[string]any, error) {
	return c.doJSON("GET", "/images/"+name+"/json", nil)
}

func (c *dockerClient) imagePull(name string) error {
	resp, err := c.do("POST", "/images/create?fromImage="+name, nil)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	io.Copy(io.Discard, resp.Body)
	if resp.StatusCode >= 400 {
		return fmt.Errorf("docker pull %s: status %d", name, resp.StatusCode)
	}
	return nil
}

func (c *dockerClient) createNetwork(opts map[string]any) (string, error) {
	body, _ := json.Marshal(opts)
	result, err := c.doJSON("POST", "/networks/create", bytes.NewReader(body))
	if err != nil {
		return "", err
	}
	if id, ok := result["Id"].(string); ok {
		return id, nil
	}
	return "", nil
}

func (c *dockerClient) removeNetwork(nameOrID string) error {
	resp, err := c.do("DELETE", "/networks/"+nameOrID, nil)
	if err != nil {
		return err
	}
	resp.Body.Close()
	if resp.StatusCode >= 400 && resp.StatusCode != 404 {
		return fmt.Errorf("docker network remove %s: status %d", nameOrID, resp.StatusCode)
	}
	return nil
}

func (c *dockerClient) createContainer(opts map[string]any) (string, error) {
	name, _ := opts["name"].(string)
	delete(opts, "name")

	body, _ := json.Marshal(opts)
	path := "/containers/create"
	if name != "" {
		path += "?name=" + name
	}
	result, err := c.doJSON("POST", path, bytes.NewReader(body))
	if err != nil {
		return "", err
	}
	if id, ok := result["Id"].(string); ok {
		return id, nil
	}
	return "", fmt.Errorf("docker createContainer: no Id in response")
}

func (c *dockerClient) startContainer(id string) error {
	resp, err := c.do("POST", "/containers/"+id+"/start", nil)
	if err != nil {
		return err
	}
	resp.Body.Close()
	if resp.StatusCode >= 400 && resp.StatusCode != 304 {
		return fmt.Errorf("docker start %s: status %d", id, resp.StatusCode)
	}
	return nil
}

func (c *dockerClient) stopContainer(id string, timeout int) error {
	path := fmt.Sprintf("/containers/%s/stop?t=%d", id, timeout)
	resp, err := c.do("POST", path, nil)
	if err != nil {
		return err
	}
	resp.Body.Close()
	if resp.StatusCode >= 400 && resp.StatusCode != 304 {
		return fmt.Errorf("docker stop %s: status %d", id, resp.StatusCode)
	}
	return nil
}

func (c *dockerClient) removeContainer(id string, force bool) error {
	path := fmt.Sprintf("/containers/%s?force=%v", id, force)
	resp, err := c.do("DELETE", path, nil)
	if err != nil {
		return err
	}
	resp.Body.Close()
	if resp.StatusCode >= 400 && resp.StatusCode != 404 {
		return fmt.Errorf("docker remove %s: status %d", id, resp.StatusCode)
	}
	return nil
}

func (c *dockerClient) waitContainer(id string) (int, error) {
	resp, err := c.do("POST", "/containers/"+id+"/wait", nil)
	if err != nil {
		return -1, err
	}
	defer resp.Body.Close()
	var result struct {
		StatusCode int `json:"StatusCode"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return -1, err
	}
	return result.StatusCode, nil
}

func (c *dockerClient) inspectContainer(id string) (map[string]any, error) {
	return c.doJSON("GET", "/containers/"+id+"/json", nil)
}

func (c *dockerClient) containerLogs(id string, follow bool) (io.ReadCloser, error) {
	followStr := "false"
	if follow {
		followStr = "true"
	}
	path := fmt.Sprintf("/containers/%s/logs?follow=%s&stdout=true&stderr=true", id, followStr)
	resp, err := c.do("GET", path, nil)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode >= 400 {
		data, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		return nil, fmt.Errorf("docker logs %s: %s (status %d)", id, string(data), resp.StatusCode)
	}
	return resp.Body, nil
}

// dockerAsyncResult holds the result of an async Docker operation.
type dockerAsyncResult struct {
	Value string // JSON-encoded result or raw string
	Err   string // error message, empty on success
}

// dockerManager implements TickManager for async Docker operations.
type dockerManager struct {
	mu      sync.Mutex
	clients map[int]*dockerClient
	nextID  int
	pending map[int]dockerAsyncResult
	active  int
	wakeFn  func()
}

func newDockerManager(wakeFn func()) *dockerManager {
	return &dockerManager{
		clients: make(map[int]*dockerClient),
		nextID:  1,
		pending: make(map[int]dockerAsyncResult),
		wakeFn:  wakeFn,
	}
}

func (m *dockerManager) ProcessEvents(r *Runtime) {
	m.mu.Lock()
	if len(m.pending) == 0 {
		m.mu.Unlock()
		return
	}
	results := m.pending
	m.pending = make(map[int]dockerAsyncResult)
	m.mu.Unlock()

	data, _ := json.Marshal(results)
	r.execLocked("if(typeof __dockerDeliverResults==='function')__dockerDeliverResults(" + string(data) + ")")
}

func (m *dockerManager) HasActive() bool {
	m.mu.Lock()
	n := m.active
	m.mu.Unlock()
	return n > 0
}

func (m *dockerManager) Close() {
	m.mu.Lock()
	m.clients = nil
	m.mu.Unlock()
}

func (m *dockerManager) addClient(c *dockerClient) int {
	m.mu.Lock()
	id := m.nextID
	m.nextID++
	m.clients[id] = c
	m.mu.Unlock()
	return id
}

func (m *dockerManager) getClient(id int) *dockerClient {
	m.mu.Lock()
	c := m.clients[id]
	m.mu.Unlock()
	return c
}

// runAsync starts a goroutine to run fn and delivers the result via the event loop.
func (m *dockerManager) runAsync(fn func() (string, error)) int {
	m.mu.Lock()
	id := m.nextID
	m.nextID++
	m.active++
	m.mu.Unlock()

	go func() {
		defer func() {
			if rec := recover(); rec != nil {
				m.mu.Lock()
				m.pending[id] = dockerAsyncResult{Err: fmt.Sprintf("panic: %v", rec)}
				m.active--
				m.mu.Unlock()
				if m.wakeFn != nil {
					m.wakeFn()
				}
			}
		}()
		val, err := fn()
		errStr := ""
		if err != nil {
			errStr = err.Error()
		}
		m.mu.Lock()
		m.pending[id] = dockerAsyncResult{Value: val, Err: errStr}
		m.active--
		m.mu.Unlock()
		if m.wakeFn != nil {
			m.wakeFn()
		}
	}()

	return id
}

// DockerModule returns an Option that installs the Docker native module.
// The module is lazy-initialized on first require('dockerode').
func DockerModule() Option {
	return func(c *config) {
		c.modules = append(c.modules, Module{
			Name: "dockerode",
			Init: installDockerModule,
		})
	}
}

func installDockerModule(r *Runtime) error {
	var mgr *dockerManager
	var initOnce sync.Once

	ensureInit := func() {
		initOnce.Do(func() {
			mgr = newDockerManager(r.Wake)
			r.customTickMgrs = append(r.customTickMgrs, mgr)
		})
	}

	// __docker_connect is the only synchronous callback (creates the client).
	if err := r.registerFuncLocked("__docker_connect", func(args []any) (any, error) {
		ensureInit()
		socketPath := ""
		if len(args) > 0 {
			if s, ok := args[0].(string); ok {
				socketPath = s
			}
		}
		id := mgr.addClient(newDockerClient(socketPath))
		return float64(id), nil
	}); err != nil {
		return err
	}

	// __docker_op dispatches all async Docker operations.
	// args: [clientID, opName, ...opArgs]
	// Returns: opID (float64) for Promise resolution via __dockerDeliverResults.
	if err := r.registerFuncLocked("__docker_op", func(args []any) (any, error) {
		if mgr == nil {
			return nil, errInvalidDockerClient
		}
		if len(args) < 2 {
			return nil, fmt.Errorf("docker: op requires clientID and opName")
		}
		clientID, _ := args[0].(float64)
		opName, _ := args[1].(string)
		c := mgr.getClient(int(clientID))
		if c == nil {
			return nil, errInvalidDockerClient
		}

		var opFn func() (string, error)

		switch opName {
		case "ping":
			opFn = func() (string, error) { return "", c.ping() }
		case "imageInspect":
			name, _ := args[2].(string)
			opFn = func() (string, error) {
				info, err := c.imageInspect(name)
				if err != nil {
					return "", err
				}
				d, _ := json.Marshal(info)
				return string(d), nil
			}
		case "imagePull":
			name, _ := args[2].(string)
			opFn = func() (string, error) { return "", c.imagePull(name) }
		case "networkCreate":
			optsJSON, _ := args[2].(string)
			opFn = func() (string, error) {
				var opts map[string]any
				if err := json.Unmarshal([]byte(optsJSON), &opts); err != nil {
					return "", fmt.Errorf("docker: invalid options: %w", err)
				}
				return c.createNetwork(opts)
			}
		case "networkRemove":
			name, _ := args[2].(string)
			opFn = func() (string, error) { return "", c.removeNetwork(name) }
		case "containerCreate":
			optsJSON, _ := args[2].(string)
			opFn = func() (string, error) {
				var opts map[string]any
				if err := json.Unmarshal([]byte(optsJSON), &opts); err != nil {
					return "", fmt.Errorf("docker: invalid options: %w", err)
				}
				return c.createContainer(opts)
			}
		case "containerStart":
			id, _ := args[2].(string)
			opFn = func() (string, error) { return "", c.startContainer(id) }
		case "containerStop":
			id, _ := args[2].(string)
			t := 10
			if len(args) > 3 {
				if v, ok := args[3].(float64); ok {
					t = int(v)
				}
			}
			opFn = func() (string, error) { return "", c.stopContainer(id, t) }
		case "containerRemove":
			id, _ := args[2].(string)
			force := false
			if len(args) > 3 {
				force, _ = args[3].(bool)
			}
			opFn = func() (string, error) { return "", c.removeContainer(id, force) }
		case "containerWait":
			id, _ := args[2].(string)
			opFn = func() (string, error) {
				code, err := c.waitContainer(id)
				if err != nil {
					return "", err
				}
				return strconv.Itoa(code), nil
			}
		case "containerInspect":
			id, _ := args[2].(string)
			opFn = func() (string, error) {
				info, err := c.inspectContainer(id)
				if err != nil {
					return "", err
				}
				d, _ := json.Marshal(info)
				return string(d), nil
			}
		case "containerLogs":
			id, _ := args[2].(string)
			follow := false
			if len(args) > 3 {
				follow, _ = args[3].(bool)
			}
			opFn = func() (string, error) {
				reader, err := c.containerLogs(id, follow)
				if err != nil {
					return "", err
				}
				streamID := r.streamMgr.createGoToJS(reader)
				return strconv.Itoa(streamID), nil
			}
		default:
			return nil, fmt.Errorf("docker: unknown op %q", opName)
		}

		opID := mgr.runAsync(opFn)
		return float64(opID), nil
	}); err != nil {
		return err
	}

	return r.execLocked(dockerModuleJS())
}

func dockerModuleJS() string {
	return `(function() {
	var __dockerPending = {};

	globalThis.__dockerDeliverResults = function(results) {
		for (var id in results) {
			var r = results[id];
			var p = __dockerPending[id];
			if (p) {
				delete __dockerPending[id];
				if (r.Err) p.reject(new Error(r.Err));
				else p.resolve(r.Value);
			}
		}
	};

	function dockerAsync(clientId, op) {
		var args = [clientId, op];
		for (var i = 2; i < arguments.length; i++) args.push(arguments[i]);
		var opId = __docker_op.apply(null, args);
		return new Promise(function(resolve, reject) {
			__dockerPending[opId] = { resolve: resolve, reject: reject };
		});
	}

	function Docker(opts) {
		opts = opts || {};
		this._id = __docker_connect(opts.socketPath || '');
	}

	Docker.prototype.ping = function() {
		return dockerAsync(this._id, 'ping').then(function() { return 'OK'; });
	};

	Docker.prototype.getImage = function(name) {
		var id = this._id;
		return {
			inspect: function() {
				return dockerAsync(id, 'imageInspect', name).then(JSON.parse);
			}
		};
	};

	Docker.prototype.pull = function(image, callback) {
		var p = dockerAsync(this._id, 'imagePull', image);
		if (typeof callback === 'function') {
			p.then(function() { callback(null, { on: function() {}, pipe: function() {} }); })
			 .catch(function(e) { callback(e); });
			return;
		}
		return p;
	};

	Docker.prototype.modem = {
		followProgress: function(stream, callback) {
			if (typeof callback === 'function') callback(null);
		}
	};

	Docker.prototype.createNetwork = function(opts) {
		var id = this._id;
		return dockerAsync(id, 'networkCreate', JSON.stringify(opts)).then(function(netId) {
			return { id: netId, remove: function() { return dockerAsync(id, 'networkRemove', opts.Name || netId); } };
		});
	};

	Docker.prototype.getNetwork = function(name) {
		var id = this._id;
		return { remove: function() { return dockerAsync(id, 'networkRemove', name); } };
	};

	Docker.prototype.createContainer = function(opts) {
		var id = this._id;
		return dockerAsync(id, 'containerCreate', JSON.stringify(opts)).then(function(cid) {
			return new Container(id, cid);
		});
	};

	Docker.prototype.getContainer = function(nameOrId) {
		return new Container(this._id, nameOrId);
	};

	function Container(clientId, id) {
		this._clientId = clientId;
		this.id = id;
	}

	Container.prototype.start = function() {
		return dockerAsync(this._clientId, 'containerStart', this.id);
	};

	Container.prototype.stop = function(opts) {
		return dockerAsync(this._clientId, 'containerStop', this.id, (opts && opts.t) || 10);
	};

	Container.prototype.remove = function(opts) {
		return dockerAsync(this._clientId, 'containerRemove', this.id, !!(opts && opts.force));
	};

	Container.prototype.wait = function() {
		return dockerAsync(this._clientId, 'containerWait', this.id).then(function(code) {
			return { StatusCode: parseInt(code, 10) };
		});
	};

	Container.prototype.inspect = function() {
		return dockerAsync(this._clientId, 'containerInspect', this.id).then(JSON.parse);
	};

	Container.prototype.logs = function(opts) {
		return dockerAsync(this._clientId, 'containerLogs', this.id, !!(opts && opts.follow)).then(function(streamId) {
			return __streamCreateReadable(parseInt(streamId, 10));
		});
	};

	if (typeof globalThis.require !== 'undefined' && globalThis.require._modules) {
		globalThis.require._modules['dockerode'] = Docker;
	}
})();`
}
