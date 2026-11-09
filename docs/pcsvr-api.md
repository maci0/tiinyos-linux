# pcsvr API notes

Recovered from the shipped binaries and the Electron client. The Windows
and Apple builds are the same git revision. Function bodies are stripped,
so a path below is a route the binary registers or the app calls. The
method is listed only where the client or a script states it.

## Builds

| Binary | OS | Go | Version ldflag | Git |
|---|---|---|---|---|
| `pcsvr.exe` (Windows amd64) | windows, CGO off | 1.27.1 | `1.0.7.20261022` | `be1113a530cc3385c6411a2db6db132b235c76d4` (dirty), 2026-10-21, built 2026-10-22 18:13:32 +08 |
| `tiiny_svr` (macOS arm64) | darwin, CGO on | 1.27.1 | `1.0.7.20261022` | same revision, built 5 seconds earlier |
| `pcsvr` (macOS x86_64) | darwin | 1.24.0 | not the same build | module `pcsvr v0.0.0-20251215101532-8b01b3945d73+dirty`, December 2025 |

Source tree on the build machine: `/Users/syn/work/Tiiny/02-pc/pc-service`.
Module path `pcsvr`. Framework `github.com/zeromicro/go-zero v1.9.2`.
WebSocket library `github.com/gorilla/websocket v1.5.3`. JWT library
`github.com/golang-jwt/jwt/v4`.

macOS install (`pcsvr-apple/install_pcsvr_service.sh`):

- launchd label `ai.tiiny.pcsvr`, display name `Tiiny PC Service`
- legacy label `com.pc.pcsvr`
- binary `/usr/local/bin/tiiny_svr`
- work dir `/usr/local/share/pcsvr`
- config dir `/etc/pcsvr`
- logs `/var/log/pcsvr` and `/etc/pcsvr/logs`
- signed `Developer ID Application: TIINY AI INC (UFXWCR59YG)`
- request JSONL is capped at 100 MiB and kept 7 days

The Intel package ships an older, smaller `pcsvr` and a two-line config
(`Host: 0.0.0.0`, `Port: 60000`). The Apple package is the current one.

## Listeners

| Port | Config | Role |
|---|---|---|
| 60000 | `Host` / `Port` | HTTP API and the device websocket |
| 60053 | `DNS.ListenPort` | DNS for `tiiny.local`. `TTLSeconds` 60. `DefaultIPv4` is `127.0.0.1` until a device is selected |
| 60022 | `SSHProxy.Port` | TCP copy to the selected device's `TargetPort` (22) |
| 80 and 443 | `ConnectPage.HTTPPort`, `HTTPSPort` | "Please connect Tiiny device". `ProxyMode: device` forwards through `LocalProxyPort` 8004 |
| 60001 | `WebUI.Port` | pcsvr's own status page. `DefaultLanguage: en`. The page shows this port and the API port |
| 60002 | WireGuard `ListenPort` in `tiiny-wg.conf` | Host side of the tunnel. Address `10.0.0.1/24` and `fd81:7:7:7::1/128` |
| 39217 UDP | `DeviceDiscovery.UDPPort` | Broadcast `RequestToken` (`GADGET_DISCOVER_V1`) every `BroadcastIntervalSeconds` (1). Wait `ResponseWindowMillis` (800) |
| 39218 | `DeviceDiscovery.HTTPPort` | `GET <peer>:39218/device.json`. Timeout `HTTPTimeoutMillis` (1000). Also the USB-storage decision port |

`DeviceTTLSeconds` is 8. A device with no refresh in that window leaves
the list.

`WindowsCompatListener` (default true) is the extra DNS listener the
Windows build uses so the system resolver picks up `*.tiiny`. The Apple
config still sets it. `AutoConfigureResolver` (default true) points the
OS resolver at port 60053. DNS records are stored at
`dns_data/records.json`.

## Device discovery

UDP request body is the literal `GADGET_DISCOVER_V1`. The reply is JSON.
Fields named by the binary and the app:

```json
{
  "schema_version": "",
  "device_id": "",
  "device_name": "",
  "serial_number": "",
  "hostname": "",
  "discovery_token": "GADGET_DISCOVER_V1",
  "ipv4_addresses": [{"interface": "wlan0", "address": "192.168.1.9"}],
  "usb": {
    "interface": "usb0",
    "active": 1,
    "network": "172.29.41.120/30",
    "device_ip": "172.29.41.121",
    "host_ip": "172.29.41.122",
    "host_mac": "02:99:24:e9:fa:02"
  },
  "service": {"http_port": 39218, "http_path": "/device.json"},
  "preferred_metadata_url": "",
  "preferred_usb_metadata_url": "",
  "preferred_backend_url": "",
  "preferred_usb_backend_url": "",
  "preferred_endpoints": {}
}
```

A live box on this machine answered with both a Wi-Fi address and the
USB `/30`. The USB reply is the one that carries `host_mac`. The host
must assign `usb.host_ip` to the interface with that MAC before
`usb.device_ip` answers.

The devices websocket writes `device_added`, `device_updated`,
`device_unreachable`, and `device_selected`. The string `device_removed`
is in the binary and has no reference in that handler. The app's first
read is `{"event":"connection","data":{devices, local_devices,
selected_device}}`.

`ListDevicesLogic.applyUSBInterfaceState` marks the USB-answered device
as local. `SelectDeviceLogic.refreshDNSSelectionState` rewrites the DNS
zone to that device.

## USB storage

`USBMonitor` is the old label scan. It polls every
`PollIntervalSeconds` (2) for volumes whose label contains `tiiny`, and
ejects them. The Apple config turns it off. The comment in
`pcsvr-api.yaml` says it also ejects disks the user plugged in on
purpose, and exists only for firmware that has no `/usb-storage`.

`USBStorageBlock` replaces it. On a fresh plug, pcsvr asks the box to
keep the mass-storage function disabled for that plug. The box's own
timeout is `USB_STORAGE_DECISION_TIMEOUT`, and
`DecisionWindowSeconds` (3) must match it. Retries are
`RetryIntervalMillis` (200) against `HTTPPort` 39218. The binary names
the call path `/usb-storage` and the failure event `block_failed`.
The local `/usb-storage` reply is `ok`, `blocked`, and `error`.

`POST /account/update_usb_auto_eject` reads the eject monitor before the
action. With no monitor it returns `error_code` 50010 and
`usb auto eject monitor is unavailable`. The body is `status`,
`error_code`, `enabled`, `supported`, and `msg`.

## WireGuard

Apple only. `WireGuard.ConfigPath` is
`/usr/local/etc/wireguard/tiiny-wg.conf`. `AutoStart: true`. The bundle
ships `wg`, `wg-quick`, and `wireguard_manager.sh`, installed under
`/usr/local/lib/pcsvr/wireguard`.

`tiiny-wg.conf` puts the host at `10.0.0.1/24` and `fd81:7:7:7::1/128`,
listening on UDP 60002. The peer is `10.0.0.2/32` and
`fd81:7:7:7::10/128`, endpoint `169.254.0.1:60001`, keepalive 10 seconds.
NAT is installed with `pfctl` on `en0`. The private key is in that file
in the package.

`POST /account/update_wireguard` is the runtime switch. The Windows
binary logs `pcsvr-wg-quick`, `stop_requested`, and `stop_succeeded`.

## Handlers that exist

The Apple and Windows binaries are stripped (`-s -w`), so function
bodies are not recoverable as source. The pclntab still names every Go
file that was compiled. These are all of the HTTP handlers. There is no
`guard` package and no `statusstream` file. Earlier notes that listed
`/guard/*` and `/status/stream` were string collisions, not handlers.

| File | Route it serves |
|---|---|
| `account/updateauthkeyhandler.go` | `POST /account/update_auth_key` |
| `account/gettemporaryauthkeyhandler.go` | temporary auth key |
| `account/listauthkeyshandler.go` | list stored keys |
| `account/listtemporaryauthkeyshandler.go` | list temporary keys |
| `account/deleteauthkeyhandler.go` | delete a key |
| `account/updateusbautoejecthandler.go` | `POST /account/update_usb_auto_eject` |
| `account/updatewireguardhandler.go` | `POST /account/update_wireguard` |
| `devices/listdeviceshandler.go` | `GET /devices_list` |
| `devices/listdeviceswshandler.go` | `GET /devices_list/ws` |
| `devices/selectdevicehandler.go` | `POST /select_device` |
| `dns/listrecordshandler.go` | `GET /dns/records` |
| `dns/resolverecordshandler.go` | `/dns/resolve` |
| `dns/updaterecordhandler.go` | `/dns/update` |
| `docs/swaggerhandler.go` | swagger JSON |
| `docs/swaggeruihandler.go` | swagger UI |
| `loghandler.go` | `/log` |
| `requestdebughandler.go` | `/requests`, capture, clear |
| `debugauthhandler.go` | `/debug/unlock` |
| `statushandler.go` | `/status` |
| `versionhandler.go` | `/version` and `/pcsvr/version` |
| `routes.go` | the table that registers the rows above |

Logic files match those handlers one for one, plus
`logic/versionlogic.go`. Device discovery, DNS, SSH, USB storage,
WireGuard, the connect page, and the web UI are packages, not extra
HTTP handlers.

## Account

| Method | Path | Symbol |
|---|---|---|
| POST | `/account/update_auth_key` | `UpdateAuthKey` then `compareAuthInfo`. Account slot `+0x70` is closure `0x6d3290`. Success is status `ok`, error code 0, and message `Updated auth key for device`. The log at `0x6bd18c` is level `info`, category `auth`, event `auth_key_updated`, and the same 27-byte message. The route table also registers `POST /account/get_temporary_auth_key`, `GET` and `DELETE /account/auth_keys`, and `GET` and `DELETE /account/temporary_auth_keys` |
| | `/account/get_temporary_auth_key` | `GetTemporaryAuthKey`, `generateTemporaryAuthKey`, `isAuthKeyExpired` |
| | `/account/list_auth_keys` | `ListAuthKeys`. Each key is `device_id`, `expire_time` (RFC3339, empty when the file has no time), `expired` (`expire_time` is before now), and `key_preview`. A key of 8 bytes or fewer previews as `****`. A longer key keeps every byte except the last four and appends `****`. The list also includes `auth_data/*.json` files that are not in memory |
| | `/account/list_temporary_auth_keys` | `ListTemporaryAuthKeys`. Each key uses the same summary as `ListAuthKeys`: `device_id`, `expire_time`, `expired`, and `key_preview`. The preview is the stored token, masked by the same 8-byte rule. A nil service context takes the writer at `0x6bb2f4`: status `error`, code 50010, and the 30-byte message `service context is unavailable`. This process always has a context, so the HTTP handler does not return it |
| | `/account/delete_auth_key` | `DeleteAuthKey` |
| | `/account/delete_temporary_auth_keys` | `DeleteTemporaryAuthKeys` |
| POST | `/account/update_usb_auto_eject` | `UpdateUSBAutoEject` |
| POST | `/account/update_wireguard` | `UpdateWireGuard` |

`update_auth_key` body, from the Electron client:

```json
{"device_id": "", "auth_key": "", "user_id": "", "agent_id": "", "device_name": "", "auto_eject": false}
```

The client treats `status` other than `"ok"` as failure and throws
`msg`. `jwt/v4` is linked. The temporary-key `data` field is the
`tak|` string below, not a JWT.
`generateTemporaryAuthKey` loads `0x45d964b800` nanoseconds, which is
300 seconds. Its stack frame runs from the morestack check at file
`0x6b9a40` to `add sp, sp, #0x850; ret` at `0x6ba87c`. The pclntab entry
`0x6ba130` is inside that frame. The lookup at `0x6bab90` (returns at
`0x6baee8`) loads the stored auth key from service-context field
`0x5a8` and passes that string to `0x69db0`, which copies at most 32
bytes. The call at `0x6bad54` enters `0x290520`, which calls `0x716890`.
That body is `hmac.New`: it XORs the key with `0x36` and `0x5c`. The
hash factory is the function at `0x2600a0`, which stores the SHA-256
initialization vector `0x6a09e667, 0xbb67ae85, …` and returns the itab
at `0x109b318` whose methods are `fips140/sha256`. The message is the
8-byte big-endian counter. The signed division magic
`0x6d3a06d3a06d3a07` shifted right by 7 is division by 300. Unix
seconds, after subtracting `0xe7791f700`, sit at `[sp,#0x48]`. The
counter `floor(unix/300)` sits at `[sp,#0x58]`. The code sits at
`[sp,#0x70]`. The lookup then formats the 14-byte string
`%s|%s|%d|%s|%s` and prefixes the 4 bytes `tak|`. The five interface
values stored at `0x6baf6c` are, in order: the first five HMAC bytes,
`sprintf("%d")` of the unix seconds, the counter (the `%d`),
`sprintf("%06d")` of the code, and `sprintf("%x")` of a 4-byte copy of
the digest prefix. The success path stores that string in `data`. The response is
`status` `ok`, `error_code` 0, `data`, and an empty `msg`.
`tiiny_svr` is Mach-O arm64 and does not run here, so the mux test
recomputes the token.
After the token is stored, the function logs
`Generated temporary auth key for agent: %s, device: %s`. The key file
write fails with `failed to write auth file: %v`. That error is logged.
The HTTP body on that path is the 45-byte sentence `Failed to save
auth key to persistent storage`, code 50001. The directory is the
9-byte slice `auth_data` and the file name is `%s.json`. An empty
device id fails with `invalid auth device id: empty`. The 40002 message is the 35-byte slice `auth key time out, please try
again`. The predicate at `0x6bec30` returns true when the auth key
length is not zero, and the false path logs the 53-byte `Rejected auth
key update because the auth key expired`. The file record is
`svc.AuthInfo`: `auth_key` and `expire_time`. `expire_time` is the
`time.Time` passed to `time.Before`. A time before now returns
`Auth key expired for device: ` plus the device id. The stored
`expire_time` is 24 hours after the update.

Recovered response text and error codes, from length-prefixed slices
and the immediates stored next to `status: "error"`:

| Branch | `error_code` | `msg` |
|---|---|---|
| Temporary auth key delete, body without `device_id` or `token` | 40001 | `device_id is required` |
| Temporary auth key delete | 0 | The success struct is `status`, `error_code`, `removed`, `msg`. The writer at `0x6b985c` stores status `ok`, error code 0, `removed` as the count of keys whose device id or token matched, and an empty `msg`. An empty body removes nothing. The log at `0x6b9810` is level `info`, category `auth`, event `temporary_auth_keys_deleted`, message `Temporary auth keys deleted` |
| Auth key delete, empty body or empty `device_id` | 40001 | `device_id is required` |
| Auth key delete, id not stored and no auth file | 50011 | `failed to remove auth file for device ` plus the id. The log at `0x6b9264` is level `error`, category `auth`, event `delete_auth_key_failed`, message `Failed to delete auth key`. Success logs level `info`, category `auth`, event `auth_key_deleted`, message `Auth key deleted`, and the HTTP message is empty |
| Auth key delete, stored id | 0 | `status` `ok`, `error_code` 0, empty `msg`. The log message is `Auth key deleted` |
| Auth key, select, DNS update, or temporary-key delete body is not JSON | 40001 or 40011 | `type mismatch for field "body"` |
| Auth key, empty body | 40001 | `Rejected auth key update because request is empty` |
| Auth key, device or key missing | 40001 | `Rejected auth key update because device_id or auth_key is missing` |
| Auth key shorter than 10 bytes | 40001 | `Rejected auth key update because the auth key is too short` |
| Auth key, length predicate false | 40002 | `auth key time out, please try again`. The log data is `Rejected auth key update because the auth key expired`. The predicate at `0x6bec30` is true when the auth key length is not zero. The empty-key check earlier in the handler returns 40001 first |
| Auth key, file write failed | 50001 | `Failed to save auth key to persistent storage`. The log at `0x6bcab4` is level `error`, category `auth`, event `update_auth_key_failed`, message `Failed to persist auth key`. The file is `auth_data/<device_id>.json` |
| Auth key, saved file cannot be reloaded | 50001 | `Failed to verify auth key persistence`. The log before the writer at `0x6bce18` is level `error`, category `auth`, event `update_auth_key_failed`, message `Failed to verify persisted auth key` |
| Auth key, reloaded record differs | | The compare at `0x6bd3b0` logs level `warn`, category `auth`, event `storage_inconsistency`, message `Detected auth key storage inconsistency`, then joins the success response |
| Temporary key, empty body | 41001 | `Rejected temporary auth key request: empty request` |
| Temporary key, agent or device missing | 41001 | `Rejected temporary auth key request because agent_id or device_id is missing` |
| Temporary key, no memory key and no auth file | 50001 | `PC Service don't have auth key for device: ` plus the device id. The log event is `temporary_auth_key_failed` and its data is `Temporary auth key request failed because auth key is missing`. A missing memory key first loads `auth_data/<device_id>.json`. The file error is `auth file not found for device: %s`. A loaded file supplies `auth_key` |
| Temporary key, `expire_time` is before now | 50001 | `Auth key expired for device: ` plus the device id. The log data is `Temporary auth key request failed because auth key expired`. The file record is `auth_key` plus `expire_time` (`time.Time`). A zero time skips `time.Before`. The update stores `expire_time` 24 hours ahead |
| Temporary key, generator failed | 50002 | `Failed to generate temporary auth key` |
| USB auto eject, nil monitor (Linux) | 50010 | `usb auto eject monitor is unavailable`. The handler checks the monitor at `svc+0x600` before the action. Both nil checks write `status` `error`, `error_code` 50010, `enabled` false, `supported` false, and that 37-byte `msg`. The log is category `usb`, event `auto_eject_unavailable`, message `USB auto eject monitor is unavailable`. A present monitor still has the later writers: invalid action 40003 `action must be empty, on, or off`; unsupported platform 50011 `usb auto eject is not supported on this platform` with `supported` false; empty action status `ok`, `error_code` 0, empty `msg` |
| WireGuard, action other than `on` or `off` | 40004 | The check runs before the nil-manager check. HTTP `msg` is the 32-byte `action must be empty, on, or off`. The log at `0x6be83c` is level `warn`, category `wireguard`, event `invalid_action`, message `Rejected invalid WireGuard action` |
| WireGuard, manager missing | 50020 | `wireguard manager is unavailable`. `enabled` is false and `wireguard_status` is `Unavailable`. The log event is `manager_unavailable` |
| WireGuard, manager present and platform unsupported | 50021 | The manager method at `0x6be05c` returns false and the `tbz` at `0x6be060` reaches the writer at `0x6be9bc`. HTTP `msg` is `wireguard is not supported on this platform`. The log at `0x6be970` is level `warn`, category `wireguard`, event `unsupported`, message `WireGuard is not supported on this platform`. Linux returns false from that method, so a present manager takes this path before the script runs |
| WireGuard, start failed after the platform check | 50022 | `Failed to start WireGuard from API request` |
| WireGuard, stop failed after the platform check | 50023 | `Failed to stop WireGuard from API request` |
| WireGuard, start or stop succeeded | 0 | `Updated WireGuard state from API request` |
| DNS update, action other than `set` or `delete` | 40012 | `action must be set or delete` |
| DNS update, no host | 40010 | `host is required` |
| DNS `set` with no address | 40011 | `value is required for set action` |
| DNS update while DNS is disabled | 50031 | `dns server is disabled` |
| DNS `delete` of a host that is absent | 0 | `Delete DNS record skipped: host not present` |
| DNS `set` succeeded | 0 | `Updated DNS record` |
| DNS `delete` of a host that is present | 0 | `Deleted DNS record` |
| DNS update failed | 50032 | `Failed to update DNS record` |
| DNS delete failed | 50033 | `Failed to delete DNS record` |

`UpdateWireGuardResp` at file `0x1067450` is 56 bytes: `status`,
`error_code`, `enabled`, `supported`, `installed`, `running`,
`wireguard_status`, `msg`. The shared writer at `0x6beab0` stores those
fields and does not store `action`. `UpdateWireGuard` compares the
action with the two bytes `on` and the three bytes `off`.
`{"enabled": true}` is the same request as `{"action":"on"}`.
`IsSupported` runs before the script lookup. No manager returns 50020
`wireguard manager is unavailable`. A manager whose support method
returns false takes 50021, as documented above. The script lookup runs
only after that method returns true. Linux returns false, so the
script is not run. Start then looks up `wireguard_manager.sh`
on `PATH`, next to the binary, or at that variable. A missing script
returns
50022 and `WireGuard manager script not found`. A found script is run
with the arguments the manager uses: `install` then `start` for `on`,
and `stop` for `off`. A non-zero `install` is logged as
`install_failed` with `WireGuard installation failed`. The error Start
returns is `failed to install WireGuard before starting: %w`. The HTTP
reply stays 50022 `Failed to start WireGuard from API request`. A
non-zero `stop` returns 50023
`Failed to stop WireGuard from API request`. A zero exit returns
`Updated WireGuard state from API request` with `running` true after
start and false after stop. The shipped macOS script is the one a
production install places on that path. It calls `wg-quick` and `pfctl`.

`tiiny_user_info_refresh_failed` is the log line when the user-info
client cannot refresh. The client host string in the binary is
`compose-manager.managed.tiiny`.

## Devices, DNS, status

| Method | Path | Notes |
|---|---|---|
| GET | `/pcsvr/version` | `version`, `build_time`, `git_commit`, `go_version`, `platform`, `status` |
| GET | `/version` | Same handler, short path |
| GET | `/status` and `/pcsvr/status` | `StatusSnapshotResp`: `updated_at`, `service_status` (`running`), `tracked_auth_devices`, `version` (`version`, `build_time`, `git_commit`, `go_version`, `platform`, `status`), `dns` (`enabled`, `running`, `domain`, `listen_address`, `default_ipv4`, `record_count`), `ssh_proxy` (`status` `ok`, `error_code` 0, `enabled` and `running` from the proxy, `listen_address` `127.0.0.1:60022`, `target_port` 22, `command` `ssh tiiny@` plus the selected address, or `ssh tiiny@[address]` when the address contains a colon, empty `msg`), `usb_auto_eject` (`enabled`, `supported`, `msg`), `wireguard` (no manager: `status` `error`, `error_code` 50020, `wireguard_status` `Unavailable`, `msg` `wireguard manager is unavailable`; a manager present: `status` `ok`, `error_code` 0, empty `msg`, and the four bools from the manager), `usb_networks` |
| GET | `/status/stream` | `StatusStreamHandler`. Websocket. First text frame is the snapshot plus `event: status` |
| GET | `/debug/guard/status` and `/debug/guard/probe` | The route table registers both as GET. The response type is `status`, `error_code`, `active`, `enabled`, `msg`. A nil guard is `status` `error`, `error_code` 40302, `msg` `guard unavailable`, with `active` and `enabled` omitted. This process has no guard |
| POST | `/debug/guard/standdown` | Same 48-byte response. The nil-guard path is 40302, `guard unavailable`. The suspend call runs only when a guard pointer is present |
| GET | `/debug/pprof` | A nil pprof manager is `status` `error`, `error_code` 40302, and `msg` `guard unavailable`. `enabled` is omitted on that path |
| POST | `/debug/pprof` | `PprofToggleHandler`. Body `ping` or `heartbeat` enables it. `{"enabled": bool}` sets it. Any other body is error 40011, `invalid pprof toggle payload` |
| POST | `/requests/capture` | A body without `enabled` is error 40007, `invalid capture toggle payload`. Success is `status`, `error_code` 0, and `capture_enabled`. `msg` is omitted |
| DELETE | `/debug/requests` | The route table registers DELETE on this path. The clear writer stores `status` `ok`, `error_code` 0, and the 7-byte `msg` `cleared` |
| OPTIONS | `/account/get_temporary_auth_key`, `/account/temporary_auth_keys`, `/account/update_auth_key`, `/account/auth_keys`, `/account/update_usb_auto_eject`, `/account/update_wireguard`, `/debug/requests`, `/debug/requests/capture`, `/debug/unlock`, `/debug/pprof`, `/debug/guard/status`, `/debug/guard/standdown`, `/devices_list`, `/devices_list/ws`, `/select_device`, `/status`, `/status/stream`, `/version`, `/log`, `/dns/update_record`, `/dns/records`, `/dns/resolve` | The account table, the 28-route builder, and `RegisterHandlers` store the writer at `0x6d4070` on these records. The account stores after `+0x160` are `+0x188` OPTIONS `/account/temporary_auth_keys`, `+0x1b0` OPTIONS `/account/update_auth_key`, `+0x1d8` OPTIONS `/account/auth_keys`, `+0x200` OPTIONS `/account/update_usb_auto_eject`, and `+0x228` OPTIONS `/account/update_wireguard`. `RegisterHandlers` slot `+0x98` is OPTIONS `/dns/update_record`, slot `+0xc0` is OPTIONS `/dns/records` (path `0x742db7`), and slot `+0xe8` is OPTIONS `/dns/resolve`. The later 28-route stores are `+0x2f0` OPTIONS `/devices_list/ws`, `+0x318` OPTIONS `/select_device`, `+0x3e0` OPTIONS `/status`, `+0x408` OPTIONS `/status/stream`, `+0x430` OPTIONS `/version`, and `+0x458` OPTIONS `/log`. It returns 204 and the six headers `Access-Control-Allow-Origin` `*`, `Access-Control-Allow-Methods` `GET, POST, PUT, DELETE, OPTIONS`, `Access-Control-Allow-Headers` `Content-Type, Authorization, X-Requested-With`, `Access-Control-Allow-Credentials` `true`, `Access-Control-Expose-Headers` `Content-Length`, `Access-Control-Max-Age` `86400` |
| POST | `/debug/unlock` | Body `token` (leaf certificate), `nonce` (32 bytes, hex), `signature` (ECDSA SHA-256 over the nonce), optional `chain`. Empty body is error 40010, `invalid unlock payload`. A nonce that is not 32 bytes is error 40101, `invalid nonce length`. A token that is not a certificate is error 40101, `no certificate found`. The leaf must verify to the embedded Tiiny Dev Center root (`O=Tiiny Dev Center`, ECDSA P-256 CA, valid 2026-07-17 to 2036-07-17) and its organization must be `tiiny-debug-tool`. A chain that does not end at that root is error 40101, `unlock denied`. A nonce reused inside 300 seconds is `stale unlock request`. The success struct is `status`, `error_code`, `debug_token`, `expires_at`, `msg`. The success writer stores `status` `ok`, `error_code` 0, a 64-character hex `debug_token` from 32 `crypto/rand` bytes, and `expires_at` as unix seconds ten seconds ahead. It leaves `msg` empty. `Debug unlock granted` is the log line. The Tiiny Dev Center private key is not in the binary |
| GET | `/devices_list` | `DevicesListResp`: `status`, `error_code`, `device_count`, `selected_device`, `local_devices`, `devices`, `user_info`, `msg`. The websocket frame is separate and still starts with `event: connection` |
| GET | `/devices_list/ws` | Websocket. First frame is `event: connection` with the snapshot. Later frames are `{event, data}`. A new device id is `device_added`. An id whose `device_ip`, `device_name`, `reachable`, or `local` changed is `device_updated`, and `changed_fields` names the first three when they differ. `ipv4` is the device address list, USB first. `metadata_url` is `http://` plus that address, port 39218, and `/device.json`. The helper compares both. An id that disappeared is `device_unreachable`. A selection that fails the id-and-local compare is `device_selected`. Each of the first three carries the 184-byte device. `device_selected` carries the 64-byte device: `device_id`, `device_ip`, `device_name`, `local`, `reachable`, `user_info`. A client text frame `heartbeat` is answered with `{"event":"heartbeat","timestamp":"<RFC3339>"}` |
| POST | `/select_device` | Body `device_id`, `local`. A known device returns `Selected discovered device for default DNS resolution` plus `selected_device_id`, `device_ip`, `device_name`, `local`, `auto_selected`, and `user_info`. The zone then answers that device. An empty id returns `cleared selected device and restored default page` with those device fields empty. An unknown id is error 40440, `local device not found for device_id: ` plus the id |
| GET | `/dns/records` | `status`, `error_code`, `domain`, `default_ipv4`, `record_count`, `records`, `msg` |
| POST | `/dns/resolve` | `ResolveRecords`. Body `hosts` (or `name`). Empty is error 40020, `hosts are required`. DNS disabled is error 50030, `dns manager is unavailable`. With no device selected the zone answers `127.0.0.1` |
| POST | `/dns/update` and `/dns/update_record` | `UpdateRecord`. The registration builds both paths with the same closure. Action is `set` or `delete`. An empty action is `set`. Host is `fqdn` or `host` |

DNS record events include `record_deleted` and `record_count`. Fields
named nearby: `default_ipv4`, `metadata_url`, `include_body`.

## Debug and docs

| Path | Symbol |
|---|---|
| `/log` | `LogHandler`. Each event is `id`, `timestamp`, `level`, `category`, `event`, `message`, and `details` |
| `/debug/requests` | `RequestDebugList` on GET. The body is `status` `ok` and `guard`, the capture flag. Capture is `POST /debug/requests/capture`. DELETE is the clear writer: `status` `ok`, `error_code` 0, `msg` `cleared` |
| `/requests/capture` | `RequestDebugCapture`. A body without `enabled` is error 40007, `invalid capture toggle payload`. Success is `status`, `error_code` 0, and `capture_enabled`. `msg` is omitted |
| `/requests/clear` | Older POST path. Same clear writer as `DELETE /debug/requests` |
| `/docs/swagger` and `/docs` | Both GET paths use the same swagger handler. The document is a local JSON catalog |
| `/debug/unlock` | `DebugUnlock` |
| `/debug/pprof` | Go's standard pprof tree, plus `/debug/vars` and `/debug/events`. There is no separate guard API |

Request traces go to `logs/requests.jsonl`. Events go to
`logs/events.jsonl`. Both are under `FileLog` in the Apple config
(`EventsPath`, `RequestsPath`).

Pprof is off until toggled, then it stays up for `PprofTTLSeconds` on
`PprofPort`.

## Connect page and web UI

`ConnectPage` listens on 80 and 443 and proxies the device through
`127.0.0.1:8004`. Upstream also serves a "Please connect Tiiny device" page
at `GET /connect-page`; this port does not, since the app never requests it.
Events: `connect-page`, `connect_page`, `start_failed`.
Handler symbols: `NewConnectPageProxyHandler`, `NewHandler`.

`WebUI` listens on 60001. The page prints the web UI port and the API
port (`apiPort`, default 60000).

## Box HTTP API that pcsvr does not serve

These are on the device. On Windows and macOS they are reached through
the DNS names. On Linux every name goes to the catalog listener, which
forwards it to the discovered device. The catalog answers only a box
404 on a path it implements (empty lists only, never a fake success or
state), and, for `p8800`, `agent-services`, `chat-history` and `wifi`,
the case of no box
(`proxyOrLocal` in `pcsvr/proxy.go`). After power-up the box reports
`data_state: locked` until the user unlocks it; its other services
answer 502 until then. Confirmed against a live box on
`172.29.41.121` unless noted.

| Host | Method | Path | Result seen |
|---|---|---|---|
| `api.tiiny` | POST | `/api/v1/connect` | `status: not_auth`, `error_code: 190002`, `data_state: unlocked`, the account user name. OpenAPI says account calls moved to authd |
| `api.tiiny` | GET | `/api/v1/sys/base_urls` | Requires a POST body. Schema not fully recovered |
| `api.tiiny` | GET | `/openapi.json` | FastAPI spec. Paths include `/api/v1/connect`, `/api/v1/account/auth`, `/api/v1/apps`, `/api/v1/models/`, `/api/v1/sys/*` |
| `api.tiiny` | GET | `/api/v1/apps` | 401 without a bearer token |
| `api.tiiny` | GET | `/api/v1/models/?including_downloading=false` | 401 without a token |
| `api.tiiny` | GET | `/api/v1/models/online_models` | 404 on this firmware |
| `wifi.api.tiiny` | | `/api/v1/sys/wifi/connection_status` | Device route. Catalog must not answer it |
| `wifi.api.tiiny` | | `/api/v1/sys/wifi/connection_switch` | Turn Wi-Fi on or off |
| `wifi.api.tiiny` | | `/api/v1/sys/wifi/wifi_list` | Scan |
| `wifi.api.tiiny` | | `/api/v1/sys/wifi/wifi_connect` | Join |
| `wifi.api.tiiny` | | `/api/v1/sys/wifi/wifi_disconnect` | Leave |
| `wifi.api.tiiny` | | `/api/v1/sys/wifi/wifi_change` | Change network |
| `wifi.api.tiiny` | | `/api/v1/sys/wifi/wifi_auto_connect` | Auto-join flag |
| `chat-history.api.tiiny` | GET | `/v1/chat/conversations` | Chat history list |
| `chat-history.api.tiiny` | GET | `/conversations/{id}/messages` | One conversation |
| `agent-services.api.tiiny` | GET | `/api/services/apps/metadata` | Store list. 404 on box OS 0.1.33; pcsvr answers that 404 with the box's `api.tiiny/api/v1/apps`, the same `{app, categories, carousel}` document. The store picks the `linux-x86-64` entry, else `extension`, else `web` |
| `agent-services.api.tiiny` | GET | `/api/services` | Must be a JSON array |
| `agent-services.api.tiiny` | GET | `/api/services/resource-usage` | Sidebar meters |
| `p8800.api.tiiny` | GET | `/api/v1/npu/status` | Websocket. Frame must include `type`, `devices`, `occupants`, `cpu.total_percent`, `memory.usage_percent` |
| `p8800.api.tiiny` | GET | `/api/v1/models/running/ws` | Websocket. App sends subprotocol `bearer, <token>` and drops the socket if the handshake does not echo one |
| `hardware-upgrade.api.tiiny` | GET | `/api/v2/versions` | 404 on box OS 0.1.33; the app then uses the legacy `/api/upgrade/check`. Box only: pcsvr never answers this host |
| `hardware-upgrade.api.tiiny` | POST | `/api/upgrade/check` | `error_code: "0"`, `data.current_version` `0.1.33`, `available_version` `0.1.39`, `update_available` true (2026-11-09) |
| `hardware-upgrade.api.tiiny` | GET | `/api/upgrade/status` | `data.upgrade_process.status`. `PAUSED` makes resume throw. `IDLE` during `HW_DOWNLOADING` makes the orchestrator pause. `DOWNLOADING` and `PREPARING` keep the task |
| `hardware-upgrade.api.tiiny` | POST | `/api/upgrade/download_and_ota_prepare` | Query `target_version` |
| `auth.api.tiiny` | GET | `/api/v1/connect/ws` | Heartbeat. App treats a non-101 as `authd heartbeat websocket error` |

Online, not on the box:

| Method | URL | Body |
|---|---|---|
| POST | `https://service.online.tiiny-svr.com/update/check_update` | `{"arch":"amd64","platform":"windows","now_version":"0.0.0",...}`; no token or `sn` needed. Returns `need_update`, `newest_version`, `total_bytes`, `downloads_url` (`https://files.tiinycdn.com/update/blobs/tiinyos-<version>-windows-amd64.exe`). `platform` `linux` returns `need_update: false`. `pack/fetch.py` uses this |
| POST | `https://service.online.tiiny-svr.com/v1/upgrade/tiiny-os-version` | `{"sn":"<device id>","tiiny_os_version":"1.0.3"}`. GET returns 405. Empty or unknown `sn` returns `invalid_sn`. A third field returns `invalid_json`. A valid serial returns `{"accepted":true}` |

## Calls the Electron UI makes

Searching every renderer bundle for the route fragments shows four calls
and no others. `60001` appears only as an SVG coordinate, not as the
web UI port.

| Caller | Call |
|---|---|
| `PcServiceDeviceWebSocketService` | `GET ws://127.0.0.1:60000/devices_list/ws` |
| `store` (`HPe`) | `POST http://127.0.0.1:60000/select_device`, 15s timeout, body is the device object |
| `LoginService` (`At`) | `POST <pcsvr base>/account/update_auth_key` with `{device_id, auth_key}` |
| `UpgradeApi` | a `/version` string used for the device upgrade API, not pcsvr |

The base `http://127.0.0.1:60000` is export `T` from `envVars`. The UI
does not call `/dns/*`, `/requests`, `/debug/*`, `/log`, `/docs/*`, or
the temporary-key routes. Those handlers are in the binary for the
service itself and for operators.

## What the Electron client actually reads

The renderer client is `PcServiceDeviceWebSocketService`. It opens
`/devices_list/ws` and ignores a frame whose `event` is `heartbeat`
except to keep the socket alive. The service builds that frame as
`{"event":"heartbeat","timestamp":"<RFC3339>"}`. A client text frame
whose payload is the 9-byte word `heartbeat` gets the same reply.
Every other event is logged with `event` and `data`.

`connection` replaces the whole snapshot. `data.status` other than
`"ok"` or empty is logged as a warning, and the snapshot is still
applied. The snapshot object is:

```json
{
  "status": "ok",
  "devices": [],
  "local_devices": [],
  "selected_device": null
}
```

`devices` and `local_devices` must be arrays. Each item is copied with
`local` defaulting to false for `devices` and forced true for
`local_devices`. A cable device is `local === true` or `usb === true`.

`device_added` appends `data` to `local_devices` when it is a cable
device, otherwise to `devices`. `device_updated` moves the item between
those two lists when its cable flag changes. `device_selected` sets
`selected_device` from `data`. `device_unreachable` drops that
`device_id` out of both lists and clears `selected_device` when it
matches.

Fields read off a device item:

| Field | Use |
|---|---|
| `device_id` | Identity. Required for add, update, select, and unreachable |
| `device_name` | Display. A name matching `tiiny pocket` or `tiiny pocet`, with an optional ` (2)`, is treated as the same product name |
| `original_device_name` | Preferred over `device_name` for that product-name check |
| `device_ip` | The main process returns this as the API base. Empty or `reachable === false` yields no base URL |
| `local` | Cable versus LAN. `selected_device.local` must be a boolean or the alternate-transport lookup stops |
| `usb` | Same cable test as `local` |
| `reachable` | `false` means the main process will not call the device |

`POST /select_device` sends the device object the UI already holds
(`device_id` and `local` are the fields it logs) with a 15 second
timeout. The response is read as JSON. `status` other than `"ok"` throws
`msg`. The UI also logs `error_code`, `local`, and `auto_selected`.

`POST /account/update_auth_key` sends exactly:

```json
{"device_id": "<id>", "auth_key": "<key>"}
```

HTTP not-ok throws the response text. A `TypeError` whose message
contains `fetch` is rewritten to "Network connection lost". The JSON
body is returned to the caller and not checked for `status` in this
function. The main-process copy of the same call does require
`status: "ok"`.

The main process builds `http://<device_ip>` only when `device_ip` is a
non-empty string and `reachable` is not false. `selected_device` is read
for `device_id` and `local`.

No renderer file calls `/dns/*`, `/guard/*`, `/requests`, `/debug/*`,
`/log`, or the temporary-key routes. Those exist in the binaries and are
not on the UI path.

## Linux port

`linux/pcsvr` serves every handler named in the stripped binaries:

- discovery, device list, device websocket (including `heartbeat`), select
- DNS records, resolve, and update
- account keys, temporary JWT keys, USB auto-eject, and the WireGuard flag
- `/usb-storage`, the connect page, and the web UI page
- request capture, event log, debug unlock, pprof status, and swagger
- the device routes this firmware does not serve; device names and
  Wi-Fi paths are forwarded to the address discovery selected

WireGuard itself is not brought up. `UpdateWireGuard` validates the
action, then a missing manager returns error 50020,
`wireguard manager is unavailable`, with `wireguard_status`
`Unavailable`. The start and stop failures (50022 and 50023) are the
branches after a manager exists.
USB storage blocking records the decision and posts `{"active":0}` to
the selected device when one is selected. The host cannot eject a Linux
volume without privileges the service does not have. The temporary-key
`data` field is `tak|` plus `%s|%s|%d|%s|%s`: the first five HMAC-SHA256
bytes, the unix seconds, the counter `floor(unix/300)`, the code as
`%06d`, and the first four digest bytes as `%x`. It expires 300 seconds
after issue.
