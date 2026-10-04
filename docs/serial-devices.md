# Serial devices

A board plugged into the Mac over USB — an ESP32, an Arduino, anything that
shows up as `/dev/cu.*` — cannot be handed to the container. The toolbox reaches
it the way [mobile.md](mobile.md#the-hosts-adb-server) reaches a phone: a small
server on the host owns the device, and the tools in the container talk to it
over TCP.

## Why `--device` does not work

Docker Desktop runs every container inside a Linux VM. The Mac's USB devices
belong to macOS, and the VM never sees them, so there is no `/dev/ttyUSB0` to
map. `docker run --device /dev/cu.usbserial-…` fails, and so would anything the
toolbox could add to the container. Nothing in the toolbox needs to change for
the approach below: the container already reaches the host as
`host.docker.internal`.

## The host's RFC 2217 server

RFC 2217 is serial over telnet. Unlike a raw TCP pipe it also carries the baud
rate and the DTR/RTS control lines, and those lines are what puts an ESP32 into
its bootloader and resets it. esptool ships a server for it.

On the host:

```sh
brew install esptool                  # brings esp_rfc2217_server with it
ls /dev/cu.*                          # find the board
esp_rfc2217_server -v /dev/cu.usbserial-XXXX
```

It listens on its default port, 2217, until you stop it with Ctrl-C. Restart it
whenever you reconnect the board. While it runs it holds the serial port, so
nothing else on the host can open the board at the same time.

`Address already in use` means another process holds the port, and on Docker
Desktop that is often a container publishing it: `docker ps --format
'{{.Names}}\t{{.Ports}}'` names it. Pick another port with `-p <port>` and use
the same one in the URL below.

## Using it from the container

The tools need no install in the image: `uvx` runs them on demand.

```sh
P='rfc2217://host.docker.internal:2217?ign_set_control'
uvx esptool --port "$P" chip-id
uvx esptool --port "$P" write-flash 0x1000 firmware.bin
```

esptool prints `Failed to get VID/PID` on every reset. That is harmless: a
network URL has no USB identity, and esptool falls back to the standard reset
sequence.

Any tool built on pyserial takes the same URL, because pyserial opens it with
`serial_for_url`. A tool that insists on a device path does not.

## Boards that reset on connect

Expect every new connection to reset the board: the firmware starts over each
time a tool opens the URL. esptool does not care, it resets the board itself.
An interactive tool can. A MicroPython board whose `main.py` goes into deep
sleep straight after boot turns its UART off before `mpremote` gets its single
Ctrl-C through, and `mpremote` fails with `could not enter raw repl`. Either
give `main.py` a few seconds before it sleeps, or have the tool send Ctrl-C
repeatedly until the `>>>` prompt shows.

## Security

**The server has no authentication, and it listens on every interface.**
Anyone on the same network can connect to it, then flash the board or read
what it prints. It accepts one client at a time. Stop it when you are not using
it, and do not leave it running on a shared or public network.
