# duplicate

duplicate is a small utility tool that listen for an incoming udp stream and
duplicates this stream to multiple remote destination. Optionally, duplicate
can wait for a configured delay before starting duplicating the received packets.

usage:

```bash
$ duplicate config.toml
```

## configuration

### table [default]

* remote: tell duplicate to listen for UDP packets coming from remote address.
* nic:    when duplicate subscribe to a multicast group for its incoming packets
  and that multiple interface are avaible on the server, the nic (network interface
  controller) tells duplicate the interface with the specified identifier.
  This option is not mandatory. Duplicate will chose the default network interface
  if the option is not set or let empty.

### table [[route]]

* address: address (ip:port) of the remote host where duplicate has to forward
  the incoming stream.
* delay:   delay (in millisecond) to wait before starting to forward the incoming
  stream. If the option is not set or set to 0, duplicate will not introduce any
  delay and will start to forward the incoming stream as soon as the first packet
  come in.
* buffer:  size of the buffer to use when duplicate has to wait before forwarding
  the incoming stream. If the option is not set or set to 0, duplicate uses a
  default value of 8MB

The value of the buffer option should be choosen carefully. Indeed, if the buffer
is too short, it has been observed that the delay seems to not have any effect.

The best way to compute the "ideal" size for the buffer is:

```
buffer = (packet size * delay in seconds * number of packets per second) + extra margin

eg:
packet size       = 1316
delay             = 60s
number of packets = 100

buffer = 1316 * 60 * 100 = 7896000 bytes (~8MB)
```

### example

```toml
# listen for UDP packets coming from remote address
remote = "127.0.0.1:11111"
nic    = "eth0"

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
```
