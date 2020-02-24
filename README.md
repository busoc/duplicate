# duplicate

duplicate is a small utility tool that listen for an incoming packets stream and
duplicates this stream to multiple remote destinations. Optionally, duplicate
can wait for a configured delay before starting transmitting the incoming packets.

duplicate can be configured to listen for incoming packets either in TCP or in UDP.
Moreover, it can forward the packets stream in TCP or UDP too. This last option
is specific to each route configured to duplicate.

usage:

```bash
$ duplicate config.toml
```

## configuration

### table [default]

* address: tells duplicate to listen for UDP packets coming from remote address or
  to listen from incoming packets on the specified address.
* nic: when duplicate subscribes to a multicast group for its incoming packets
  and that multiple interface are avaible on the server, the nic (network interface
  controller) tells duplicate the interface with the specified identifier.
  This option is not mandatory. Duplicate will chose the default network interface
  if the option is not set or let empty.
* keep-listen: tells duplicate to stay listening after the TCP connection with the remote
  client is closed or encounters any errors
* certificate: TLS certificate

### table [certificate]

* cert-file: path to certificate pem encoded
* cert-key: path to key pem encode
* cert-auth: list of certificates to be used when verifying clients certificate
* policy:

### table [[route]]

* protocol: tell duplicate to use either TCP or UDP to forward the incoming stream
  to the host specified by the address. If the option is not set or set to empty
  string, the default protocol used is UDP.
* address: address (ip:port) of the remote host where duplicate has to forward
  the incoming stream.
* delay:   delay (in millisecond) to wait before starting to forward the incoming
  stream. If the option is not set or set to 0, duplicate will not introduce any
  delay and will start to forward the incoming stream as soon as the first packet
  come in.
* buffer:  size of the buffer to use when duplicate has to wait before forwarding
  the incoming stream. If the option is not set or set to 0, duplicate uses a
  default value of 8MB. When no delay is specified, this option has no effect.
* certificate: TLS certificate

### table [route.certificate]

* cert-file: path to certificate pem encoded
* cert-key: path to key pem encode
* cert-auth: list of certificates to be used when verifying server certificate
* insecure:

### example

```toml
# listen for UDP packets coming from remote address
protocol = "udp" # default value for protocol option
address  = "127.0.0.1:11111"
nic      = "eth0"

[certificate]
cert-file = "tmp/dup-srv.pem"
key-file  = "tmp/dup-srv.key"
policy    = ""
cert-auth = []

[[route]]
# delay of 5s with buffer size of ~8KB
address = "239.192.0.1:22222"
delay   = 5000
buffer  = 8192

[[route]]
# delay of 1s with buffer size of ~1KB
address = "239.192.0.1:33333"
buffer  = 1024
delay   = 1000

[[route]]
# no delay
address  = "localhost:44444"
protocol = "tcp" # duplicate is case insensitive

  [route.certificate]
  cert-file = "tmp/dup-client.pem"
  key-file  = "tmp/dup-client.key"
  cert-auth = []
  insecure  = false
```
