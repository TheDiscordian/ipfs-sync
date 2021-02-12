# ipfs-sync
[![Go Reference](https://pkg.go.dev/badge/github.com/TheDiscordian/ipfs-sync.svg)](https://pkg.go.dev/github.com/TheDiscordian/ipfs-sync)

Note: This software is very young. If you discover any bugs, please report them via the issue tracker.

`ipfs-sync` is a simple daemon which will watch files on your filesystem, mirror them to MFS, automatically update related pins, and update related IPNS keys, so you can always access your directories from the same address. You can use it to sync your documents, photos, videos, or even a website!

## Installation

You need `go` installed, with a working `GOPATH` and `$GOPATH/bin` should be added to your `$PATH` to execute the command. Binaries will be released in the near future, removing these requirements.

`go install github.com/TheDiscordian/ipfs-sync`

## Usage

The only required parameters is `dirs`, which can be specified in the config file, or as an argument.

```
Usage of ipfs-sync:
  -basepath string
        relative MFS directory path (default "/ipfs-sync/")
  -config string
        path to config file to use
  -dirs value
        set the dirs to monitor in json format like: [{"Key":"Example1", "Dir":"/home/user/Documents/"},{"Key":"Example2", "Dir":"/home/user/Pictures/"}]
  -endpoint string
        node to connect to over HTTP (default "http://127.0.0.1:5001")
  -ignore value
        set the suffixes to ignore (default: ["kate-swp", "swp", "part"])
  -license
        display license and exit
  -sync duration
        time to sleep between IPNS syncs (ex: 120s) (default 10s)
```

`ipfs-sync` can be setup and used as a service. Simply point it to a config file, and restart it whenever the config is updated. An example config file can be found at `config.json.sample`.


## Example

Getting started is simple. The only required field is `dirs`, so if we wanted to sync a folder, we'd simply run:

```
ipfs-sync -dirs '[{"Key":"ExampleKey", "Dir":"/home/user/Documents/ExampleFolder/"}]'
2021/02/12 18:03:38 ipfs-sync starting up...
2021/02/12 18:03:38 ExampleKey not found, generating...
2021/02/12 18:03:38 Adding file to /ipfs-sync/ExampleFolder/index.html ...
2021/02/12 18:04:40 ExampleKey loaded: k51qzi5uqu5dlpvinw1zhxzo4880ge5hg9tp3ao4ye3aujdru9rap2h7izk5lm
```

This command will first check if there's a key named `ExampleKey` in `ipfs-sync`'s namespace, if not, it'll generate and return one. In this example, it synced a simple website to `k51qzi5uqu5dlpvinw1zhxzo4880ge5hg9tp3ao4ye3aujdru9rap2h7izk5lm`. As you add/remove/change files in the directory now, they'll be visible live at that address.
