# ipfs-sync
[![Go Reference](https://pkg.go.dev/badge/github.com/TheDiscordian/ipfs-sync.svg)](https://pkg.go.dev/github.com/TheDiscordian/ipfs-sync)

*Note: This software is very young. If you discover any bugs, please report them via the issue tracker.*

`ipfs-sync` is a simple daemon which will watch files on your filesystem, mirror them to MFS, automatically update related pins, and update related IPNS keys, so you can always access your directories from the same address. You can use it to sync your documents, photos, videos, or even a website!

<a href="https://www.buymeacoffee.com/trdiscordian" target="_blank"><img src="https://cdn.buymeacoffee.com/buttons/default-orange.png" alt="Buy Me A Coffee" height="41" width="174"></a>

## Installation

If your OS or architechture isn't supported, please open an issue! If it's easily supported with Go, I'll definitely consider it 😊.

### Binary

If you're on an Arch based distro, `ipfs-sync` is available on the [AUR](https://aur.archlinux.org/packages/ipfs-sync/).

Binaries are available on the [releases](https://github.com/TheDiscordian/ipfs-sync/releases) page for other distros and OSs.

### Source

You need `go` installed, with a working `GOPATH` and `$GOPATH/bin` should be added to your `$PATH` to execute the command.

`go install github.com/TheDiscordian/ipfs-sync`

## Usage

The only required parameter is `dirs`, which can be specified in the config file, or as an argument. The `ID` parameter is simply a unique idenifier for you to remember, the IPNS key will be generated using this ID.

It's recommended you either use the included systemd user-service, or run `ipfs-sync` with a command like `ipfs-sync -config $HOME/.ipfs-sync.yaml -db $HOME/.ipfs-sync.db`, after placing a config file in `~/.ipfs-sync.yaml`.

```bash
Usage of ipfs-sync:
  -basepath string
        relative MFS directory path (default "/ipfs-sync/")
  -config string
        path to config file to use
  -copyright
        display copyright and exit
  -db string
        path to file where db should be stored (example: "/home/user/.ipfs-sync.db")
  -dirs value
        set the dirs to monitor in json format like: [{"ID":"Example1", "Dir":"/home/user/Documents/", "Nocopy": false},{"ID":"Example2", "Dir":"/home/user/Pictures/", "Nocopy": false}]
  -endpoint string
        node to connect to over HTTP (default "http://127.0.0.1:5001")
  -ignore value
        set the suffixes to ignore (default: ["kate-swp", "swp", "part", "crdownload"])
  -ignorehidden
        ignore anything prefixed with "."
  -sync duration
        time to sleep between IPNS syncs (ex: 120s) (default 10s)
  -timeout duration
        longest time to wait for API calls like version and `files/mkdir` (ex: 60s) (default 30s)
  -v    display verbose output
  -version
        display version and exit
```

`ipfs-sync` can be setup and used as a service. Simply point it to a config file, and restart it whenever the config is updated. An example config file can be found at `config.yaml.sample`.


## Example

Getting started is simple. The only required field is `dirs`, so if we wanted to sync a folder, we'd simply run:

```
ipfs-sync -dirs '[{"ID":"ExampleID", "Dir":"/home/user/Documents/ExampleFolder/", "Nocopy": false}]'
2021/02/12 18:03:38 ipfs-sync starting up...
2021/02/12 18:03:38 ExampleID not found, generating...
2021/02/12 18:03:38 Adding file to /ipfs-sync/ExampleFolder/index.html ...
2021/02/12 18:04:40 ExampleID loaded: k51qzi5uqu5dlpvinw1zhxzo4880ge5hg9tp3ao4ye3aujdru9rap2h7izk5lm
```

This command will first check if there's a key named `ExampleID` in `ipfs-sync`'s namespace, if not, it'll generate and return one. In this example, it synced a simple website to `k51qzi5uqu5dlpvinw1zhxzo4880ge5hg9tp3ao4ye3aujdru9rap2h7izk5lm`. As you add/remove/change files in the directory now, they'll be visible live at that address.

The `Nocopy` option enables the `--nocopy` option when adding files for that shared directory, more info about the option can be found [here](https://docs.ipfs.io/reference/http/api/#api-v0-add), and it requires the [ipfs filestore experimental feature](https://github.com/ipfs/go-ipfs/blob/master/docs/experimental-features.md#ipfs-filestore) enabled.
