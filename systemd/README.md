# ipfs-sync systemd user service

## Setup

1. Ensure `ipfs-sync` is in your path.
	- Alternatively, edit `./user/ipfs-sync.service`, and modify the `ExecStart` line to your liking.
2. Copy the sample config file `config.yaml.sample` to `~/.ipfs-sync.yaml`, and edit it to your liking.
3. Copy the service file located in `./user/` to `~/.config/systemd/user/` (create the directory if it doesn't exist).
4. (Optional) Enable auto-starting the `ipfs-sync` daemon with `systemctl --user enable ipfs-sync`
5. Start the `ipfs-sync` daemon with `systemctl --user start ipfs-sync`
6. (Optional) Verify the daemon is running with `systemctl --user status ipfs-sync`

### Tip

If you make a configuration change, don't forget to restart the daemon with `systemctl --user restart ipfs-sync`.
