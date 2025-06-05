# narwal-staging

## Installation

0. Order a KVM from Hetzner for 3h (see below)
1. Enable rescue mode on the server, note the root password
2. Reset the machine
3. Run the bootstrap.sh script
4. Now reset and enter the BIOS menu (Delete key) on boot
5. Go to the boot options and put the UEFI Disk first.
6. Save and reboot

NOTE: from that point, you won't be able to put the server in rescue mode, without reverting the BIOS setting.
