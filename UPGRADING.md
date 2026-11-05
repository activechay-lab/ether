# Upgrading to Ether 0.3.4

Upgrade an existing 0.3.x or 0.2.x installation in place using the same installation method and data directory. Keep a backup of your database, configuration and credential key before upgrading. Plex identity, viewers, watch history, collections, profiles and source settings are stored in that directory.

## Install 0.3.4

- **In-app updater:** Open **Maintenance › Updates**, select your release channel and install 0.3.4 when offered.
- **macOS:** Quit Ether, replace the app in `Applications` using the new DMG and launch it.
- **Windows:** Run `EtherSetup-0.3.4.exe`; it updates the service while preserving `%ProgramData%\Ether`.
- **Linux:** Install the 0.3.4 `.deb` or `.rpm` with your package manager; keep `/var/lib/ether`.
- **Docker:** Pull `ghcr.io/braedonsaunders/ether:0.3.4` and recreate the container with the same config and cache volumes.

On first start, Ether applies pending database migrations. SQLite upgrades take a pre-migration snapshot; keep an independent backup too. PostgreSQL requires your own database backup. Allow startup to finish before restarting again. The `/ether/` startup status and recovery pages show migration progress or failures.

For a PostgreSQL cluster, back up the shared database and every node's configuration and credential key, stop all nodes, upgrade them together, let the writer migrate, and check that every serving node is on 0.3.4 before restoring traffic. In particular, a 0.2.x to 0.3.x upgrade changes identity and session tables and must not run with old writers connected.

## Find settings in the current console

Sidebar group headings such as **Library**, **Streaming** and **Server** organize pages; they are not extra tabs.

| Task | Console destination |
| --- | --- |
| Quality profiles and ranking | **Quality › Profiles** |
| Viewer and device profile rules | **Quality › Rules** |
| Search timing and health samples | **Quality › Searching** |
| Blocked releases | **Quality › Blocklist** |
| Source accounts and indexers | **Sources › Debrid**, **Usenet**, **Indexers** |
| Source request and provider usage | **Sources › Stats** |
| Movie release visibility and monitoring (formerly Sources › Availability) | **Catalog › Settings › General › New releases** |
| Library visibility rules | **Catalog › Settings › Rules** |
| Per-viewer preferences | **Viewers › viewer › Audio & subtitles** or **Limits** |
| Household track preferences | **Playback › Audio & subtitles** |
| Protocols and connection addresses | **Apps**; listener ports under **General › How apps connect** |
| Updates | **Maintenance › Updates** |
| Snapshots and restore | **Maintenance › Backups & import › Backups** |

Existing profiles keep their ranking order. Review **Quality › Profiles** only if you want to change it. New-release visibility offers **When they can play**, **On digital release** and **Right away**. See the [user guide](docs/user-guide.md) for configuration details.

## Additional apps and watch sync

Existing Plex installations retain their Plex configuration. Enable additional protocols under **Apps**. Jellyfin, Emby and Silo use local Ether credentials or supported device pairing, rather than a Plex password typed into those apps. Viewers manage sign-in methods and app credentials at `/ether/me`.

The default dedicated TCP ports are 8096 (Jellyfin), 8097 (Emby) and 8090 (Silo). Shared-port paths `/jellyfin`, `/emby` and `/silo` also work on the main listener. Check **General › How apps connect** for the actual addresses. Remote access for these apps needs a VPN or HTTPS reverse proxy; it does not use plex.tv routing. See [supported apps](docs/apps.md) and [installation](docs/install.md).

0.3.4 adds optional sync with an owner-run Plex Media Server under **Integrations › Plex Media Servers**. Add a server and explicitly map its accounts to Ether viewers before enabling a direction. This is separate from Plex account watch sync. See [watch sync](docs/user-guide.md#sync-watched-state-with-your-plex-media-server).

## Rollback

Do not run an older binary against a database migrated by 0.3.4. Stop all Ether processes, restore the database, configuration and credential key from the same pre-upgrade backup, install the matching older release, then restart. For SQLite, **Maintenance › Backups & import** or startup recovery can restore a snapshot; stop the newer binary before it migrates the restored database again. For PostgreSQL, restore your database dump with all nodes stopped and restore each node's matching configuration before restarting the older release.
