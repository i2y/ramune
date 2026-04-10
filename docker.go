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
	"strings"
	"sync"
	"time"
)

var errInvalidDockerClient = fmt.Errorf("docker: invalid client")

// dockerClient wraps Docker Engine API calls over Unix socket or TCP.
type dockerClient struct {
	httpClient *http.Client
	baseURL    string // e.g. "http://localhost"
}

// newDockerClient creates a client connected via Unix socket or TCP.
func newDockerClient(socketPath string) *dockerClient {
	if socketPath == "" {
		// Check DOCKER_HOST env var.
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
			Timeout: 0, // no timeout for streaming operations
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
	delete(opts, "name") // name is a query param, not body field

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
	if resp.StatusCode >= 400 && resp.StatusCode != 304 { // 304 = already started
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
	if resp.StatusCode >= 400 && resp.StatusCode != 304 { // 304 = already stopped
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

// containerLogs returns an io.ReadCloser for streaming container logs.
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

// dockerModuleState holds per-Runtime state for the Docker module.
type dockerModuleState struct {
	mu      sync.Mutex
	clients map[int]*dockerClient
	nextID  int
}

// DockerModule returns an Option that installs the Docker native module.
// JS code can `require('dockerode')` to get the Docker client.
func DockerModule() Option {
	return func(c *config) {
		c.modules = append(c.modules, Module{
			Name: "dockerode",
			Init: installDockerModule,
		})
	}
}

func installDockerModule(r *Runtime) error {
	state := &dockerModuleState{
		clients: make(map[int]*dockerClient),
		nextID:  1,
	}

	reg := func(name string, fn GoFunc) error {
		return r.registerFuncLocked(name, fn)
	}
	withClient := func(args []any, fn func(*dockerClient) (any, error)) (any, error) {
		c := state.getClient(args)
		if c == nil {
			return nil, errInvalidDockerClient
		}
		return fn(c)
	}

	if err := reg("__docker_connect", func(args []any) (any, error) {
		socketPath := ""
		if len(args) > 0 {
			if s, ok := args[0].(string); ok {
				socketPath = s
			}
		}
		client := newDockerClient(socketPath)
		state.mu.Lock()
		id := state.nextID
		state.nextID++
		state.clients[id] = client
		state.mu.Unlock()
		return float64(id), nil
	}); err != nil {
		return err
	}
	if err := reg("__docker_ping", func(args []any) (any, error) {
		return withClient(args, func(c *dockerClient) (any, error) {
			return nil, c.ping()
		})
	}); err != nil {
		return err
	}
	if err := reg("__docker_image_inspect", func(args []any) (any, error) {
		return withClient(args, func(c *dockerClient) (any, error) {
			name, _ := args[1].(string)
			result, err := c.imageInspect(name)
			if err != nil {
				return nil, err
			}
			data, _ := json.Marshal(result)
			return string(data), nil
		})
	}); err != nil {
		return err
	}
	if err := reg("__docker_image_pull", func(args []any) (any, error) {
		return withClient(args, func(c *dockerClient) (any, error) {
			name, _ := args[1].(string)
			return nil, c.imagePull(name)
		})
	}); err != nil {
		return err
	}
	if err := reg("__docker_network_create", func(args []any) (any, error) {
		return withClient(args, func(c *dockerClient) (any, error) {
			optsJSON, _ := args[1].(string)
			var opts map[string]any
			json.Unmarshal([]byte(optsJSON), &opts)
			return c.createNetwork(opts)
		})
	}); err != nil {
		return err
	}
	if err := reg("__docker_network_remove", func(args []any) (any, error) {
		return withClient(args, func(c *dockerClient) (any, error) {
			name, _ := args[1].(string)
			return nil, c.removeNetwork(name)
		})
	}); err != nil {
		return err
	}
	if err := reg("__docker_container_create", func(args []any) (any, error) {
		return withClient(args, func(c *dockerClient) (any, error) {
			optsJSON, _ := args[1].(string)
			var opts map[string]any
			json.Unmarshal([]byte(optsJSON), &opts)
			return c.createContainer(opts)
		})
	}); err != nil {
		return err
	}
	if err := reg("__docker_container_start", func(args []any) (any, error) {
		return withClient(args, func(c *dockerClient) (any, error) {
			id, _ := args[1].(string)
			return nil, c.startContainer(id)
		})
	}); err != nil {
		return err
	}
	if err := reg("__docker_container_stop", func(args []any) (any, error) {
		return withClient(args, func(c *dockerClient) (any, error) {
			id, _ := args[1].(string)
			timeout := 10
			if len(args) > 2 {
				if t, ok := args[2].(float64); ok {
					timeout = int(t)
				}
			}
			return nil, c.stopContainer(id, timeout)
		})
	}); err != nil {
		return err
	}
	if err := reg("__docker_container_remove", func(args []any) (any, error) {
		return withClient(args, func(c *dockerClient) (any, error) {
			id, _ := args[1].(string)
			force := false
			if len(args) > 2 {
				force, _ = args[2].(bool)
			}
			return nil, c.removeContainer(id, force)
		})
	}); err != nil {
		return err
	}
	if err := reg("__docker_container_wait", func(args []any) (any, error) {
		return withClient(args, func(c *dockerClient) (any, error) {
			id, _ := args[1].(string)
			code, err := c.waitContainer(id)
			if err != nil {
				return nil, err
			}
			return float64(code), nil
		})
	}); err != nil {
		return err
	}
	if err := reg("__docker_container_inspect", func(args []any) (any, error) {
		return withClient(args, func(c *dockerClient) (any, error) {
			id, _ := args[1].(string)
			result, err := c.inspectContainer(id)
			if err != nil {
				return nil, err
			}
			data, _ := json.Marshal(result)
			return string(data), nil
		})
	}); err != nil {
		return err
	}
	if err := reg("__docker_container_logs", func(args []any) (any, error) {
		return withClient(args, func(c *dockerClient) (any, error) {
			id, _ := args[1].(string)
			follow := false
			if len(args) > 2 {
				follow, _ = args[2].(bool)
			}
			reader, err := c.containerLogs(id, follow)
			if err != nil {
				return nil, err
			}
			streamID := r.streamMgr.createGoToJS(reader)
			return float64(streamID), nil
		})
	}); err != nil {
		return err
	}

	return r.execLocked(dockerModuleJS())
}

func (s *dockerModuleState) getClient(args []any) *dockerClient {
	if len(args) < 1 {
		return nil
	}
	id, ok := args[0].(float64)
	if !ok {
		return nil
	}
	s.mu.Lock()
	c := s.clients[int(id)]
	s.mu.Unlock()
	return c
}

func dockerModuleJS() string {
	return `(function() {
	function Docker(opts) {
		opts = opts || {};
		var socketPath = opts.socketPath || '';
		this._id = __docker_connect(socketPath);
	}

	Docker.prototype.ping = function() {
		var id = this._id;
		return new Promise(function(resolve, reject) {
			try { __docker_ping(id); resolve('OK'); }
			catch(e) { reject(e); }
		});
	};

	Docker.prototype.getImage = function(name) {
		var id = this._id;
		return {
			inspect: function() {
				return new Promise(function(resolve, reject) {
					try {
						var data = __docker_image_inspect(id, name);
						resolve(JSON.parse(data));
					} catch(e) { reject(e); }
				});
			}
		};
	};

	Docker.prototype.pull = function(image, callback) {
		var id = this._id;
		if (typeof callback === 'function') {
			try {
				__docker_image_pull(id, image);
				callback(null, { on: function() {}, pipe: function() {} });
			} catch(e) { callback(e); }
			return;
		}
		return new Promise(function(resolve, reject) {
			try { __docker_image_pull(id, image); resolve(); }
			catch(e) { reject(e); }
		});
	};

	Docker.prototype.modem = {
		followProgress: function(stream, callback) {
			if (typeof callback === 'function') callback(null);
		}
	};

	Docker.prototype.createNetwork = function(opts) {
		var id = this._id;
		return new Promise(function(resolve, reject) {
			try {
				var netId = __docker_network_create(id, JSON.stringify(opts));
				resolve({ id: netId, remove: function() {
					return new Promise(function(res, rej) {
						try { __docker_network_remove(id, opts.Name || netId); res(); }
						catch(e) { rej(e); }
					});
				}});
			} catch(e) { reject(e); }
		});
	};

	Docker.prototype.getNetwork = function(name) {
		var id = this._id;
		return {
			remove: function() {
				return new Promise(function(resolve, reject) {
					try { __docker_network_remove(id, name); resolve(); }
					catch(e) { reject(e); }
				});
			}
		};
	};

	Docker.prototype.createContainer = function(opts) {
		var id = this._id;
		return new Promise(function(resolve, reject) {
			try {
				var containerId = __docker_container_create(id, JSON.stringify(opts));
				resolve(new Container(id, containerId));
			} catch(e) { reject(e); }
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
		var cid = this._clientId, id = this.id;
		return new Promise(function(resolve, reject) {
			try { __docker_container_start(cid, id); resolve(); }
			catch(e) { reject(e); }
		});
	};

	Container.prototype.stop = function(opts) {
		var cid = this._clientId, id = this.id;
		var t = (opts && opts.t) || 10;
		return new Promise(function(resolve, reject) {
			try { __docker_container_stop(cid, id, t); resolve(); }
			catch(e) { reject(e); }
		});
	};

	Container.prototype.remove = function(opts) {
		var cid = this._clientId, id = this.id;
		var force = opts && opts.force;
		return new Promise(function(resolve, reject) {
			try { __docker_container_remove(cid, id, !!force); resolve(); }
			catch(e) { reject(e); }
		});
	};

	Container.prototype.wait = function() {
		var cid = this._clientId, id = this.id;
		return new Promise(function(resolve, reject) {
			try {
				var code = __docker_container_wait(cid, id);
				resolve({ StatusCode: code });
			} catch(e) { reject(e); }
		});
	};

	Container.prototype.inspect = function() {
		var cid = this._clientId, id = this.id;
		return new Promise(function(resolve, reject) {
			try {
				var data = __docker_container_inspect(cid, id);
				resolve(JSON.parse(data));
			} catch(e) { reject(e); }
		});
	};

	Container.prototype.logs = function(opts) {
		var cid = this._clientId, id = this.id;
		var follow = opts && opts.follow;
		return new Promise(function(resolve, reject) {
			try {
				var streamId = __docker_container_logs(cid, id, !!follow);
				var stream = __streamCreateReadable(streamId);
				resolve(stream);
			} catch(e) { reject(e); }
		});
	};

	if (typeof globalThis.require !== 'undefined' && globalThis.require._modules) {
		globalThis.require._modules['dockerode'] = Docker;
	}
})();`
}
