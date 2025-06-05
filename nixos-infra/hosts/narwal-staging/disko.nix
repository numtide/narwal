# Disk /dev/nvme0n1: 1024 GB (=> 953 GiB)
# Disk /dev/nvme1n1: 1024 GB (=> 953 GiB)
# Disk /dev/sda: 22 TB (=> 20 TiB)
# Disk /dev/sdb: 22 TB (=> 20 TiB)
# Disk /dev/sdc: 22 TB (=> 20 TiB)
# Disk /dev/sdd: 22 TB (=> 20 TiB)
let
  mirrorBoot = idx: {
    type = "disk";
    device = "/dev/nvme${idx}n1";
    content = {
      type = "gpt";
      partitions = {
        ESP = {
          size = "1G";
          type = "EF00";
          content = {
            type = "filesystem";
            format = "vfat";
            mountpoint = "/boot${idx}";
            mountOptions = [ "umask=0077" ];
          };
        };
        zfs = {
          size = "100%";
          content = {
            type = "zfs";
            pool = "rpool";
          };
        };
      };
    };
  };

  mirrorCache = drive: {
    type = "disk";
    device = "/dev/${drive}"; # Replace with actual device path
    content = {
      type = "gpt";
      partitions = {
        zfs = {
          size = "100%";
          content = {
            type = "zfs";
            pool = "cachepool";
          };
        };
      };
    };
  };

in
{
  boot.loader.grub = {
    enable = true;
    efiSupport = true;
    efiInstallAsRemovable = true;
    mirroredBoots = [
      {
        path = "/boot0";
        devices = [ "nodev" ];
      }
      {
        path = "/boot1";
        devices = [ "nodev" ];
      }
    ];
  };

  disko.devices = {
    disk = {
      nvme0 = mirrorBoot "0";
      nvme1 = mirrorBoot "1";
      hdd1 = mirrorCache "sda";
      hdd2 = mirrorCache "sdb";
      hdd3 = mirrorCache "sdc";
      hdd4 = mirrorCache "sdd";
    };

    zpool = {
      rpool = {
        type = "zpool";
        mode = "mirror";
        mountpoint = "/";
        rootFsOptions = {
          compression = "lz4";
          atime = "off";
        };
        datasets = {
          nix = {
            type = "zfs_fs";
            mountpoint = "/nix";
          };
          var = {
            type = "zfs_fs";
            mountpoint = "/var";
          };
          home = {
            type = "zfs_fs";
            mountpoint = "/home";
          };
        };
      };
      cachepool = {
        type = "zpool";
        mode = "raidz1";
        mountpoint = "/var/cache";
        rootFsOptions = {
          compression = "lz4";
          atime = "off";
        };
        datasets = {
          files = {
            type = "zfs_fs";
            mountpoint = "/var/cache/files";
          };
        };
      };
    };
  };
}
