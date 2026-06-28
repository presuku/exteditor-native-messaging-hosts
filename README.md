[![CI](https://github.com/presuku/exteditor-native-messaging-hosts/actions/workflows/ci.yml/badge.svg)](https://github.com/presuku/exteditor-native-messaging-hosts/actions/workflows/ci.yml)

# External Editor Native Messaging Hosts

External Editor Native Messaging Hosts for [exteditor-webext](https://github.com/presuku/exteditor-webext) and [exteditor-mailext](https://github.com/presuku/exteditor-mailext).

## Installation / Uninstallation

Download the appropriate ZIP file for your environment from the [Release](https://github.com/presuku/exteditor-native-messaging-hosts/releases) page and extract (unzip) it.

### For Windows

#### Installation

Run `install.bat`.

This script will automatically perform the following actions:

* Copy `exteditor.json` and `exteditor.exe` to `%LOCALAPPDATA%\exteditor-nmh`.
* Add the JSON file path to the registry at `HKCU\Software\Mozilla\NativeMessagingHosts\exteditor`.

#### Uninstallation

Run `uninstall.bat` to revert the changes made by the installation script.

### For Linux

#### Installation

Run `install.sh`.

This script will automatically perform the following actions:

* Copy `exteditor.json` to `~/.mozilla/native-messaging-hosts`.
* Copy `exteditor` to `~/.local/bin/exteditor-nmh`.

#### Uninstallation

Run `uninstall.sh` to revert the changes made by the installation script.

## Usage

Triggered by Firefox and Thunderbird add-ons, this tool launches your configured text editor, watches the edited file for updates, and automatically synchronizes the changes back to the browser or mail client.

## Special Thanks

### Textern

[https://github.com/jlebon/textern](https://github.com/jlebon/textern)

Textern is Firefox add-on but similar feture of this add-on and source code of
this add-on is based on Textern.

### Extenal Editor

[https://github.com/exteditor/exteditor](https://github.com/exteditor/exteditor)

This is the project that inspired this add-on.  
Unfortunately, it is not compatible with MailExtensions and thus cannot be
installed on Thunderbird 78 or later.
