{
  config,
  lib,
  ...
}:

with lib;

let
  cfg = config.services.narwal;
in
{
  options.services.narwal = {
    enable = mkEnableOption "Narwal server";

    package = mkOption {
      type = types.package;
      default = pkgs.narwal; # this package needs to be upstreamed
      description = "The Narwal package to use";
    };

    user = mkOption {
      type = types.str;
      default = "narwal";
      description = "User account under which Narwal runs";
    };

    group = mkOption {
      type = types.str;
      default = "narwal";
      description = "Group under which Narwal runs";
    };

    stateDir = mkOption {
      type = types.str;
      default = "/var/lib/narwal";
      description = "Directory to store Narwal state and LRU cache";
    };

    listenAddress = mkOption {
      type = types.str;
      default = "127.0.0.1";
      description = "Address on which Narwal listens";
    };

    port = mkOption {
      type = types.port;
      default = 8080;
      description = "Port on which Narwal listens";
    };

    database = {
      host = mkOption {
        type = types.str;
        default = "localhost";
        description = "PostgreSQL database host";
      };

      port = mkOption {
        type = types.port;
        default = 5432;
        description = "PostgreSQL database port";
      };

      name = mkOption {
        type = types.str;
        default = "narwal";
        description = "PostgreSQL database name";
      };

      user = mkOption {
        type = types.str;
        default = "narwal";
        description = "PostgreSQL database user";
      };
    };

    gcInterval = mkOption {
      type = types.str;
      default = "monthly";
      description = "Interval for garbage collection cron job";
    };

    nginx = {
      enable = mkEnableOption "Nginx reverse proxy for Narwal";

      serverName = mkOption {
        type = types.str;
        default = "localhost";
        description = "Server name for Nginx virtual host";
      };
    };
  };

  config = mkIf cfg.enable {
    users.users.${cfg.user} = {
      isSystemUser = true;
      inherit (cfg) group;
      home = cfg.stateDir;
      createHome = true;
    };

    users.groups.${cfg.group} = { };

    systemd.services.narwal = {
      description = "Narwal Server";
      wantedBy = [ "multi-user.target" ];
      after = [
        "network.target"
        "postgresql.service"
      ];
      wants = [ "postgresql.service" ];

      serviceConfig = {
        Type = "simple";
        User = cfg.user;
        Group = cfg.group;
        WorkingDirectory = cfg.stateDir;
        StateDirectory = baseNameOf cfg.stateDir;
        Restart = "always";
        RestartSec = "10s";

        # Security settings
        NoNewPrivileges = true;
        PrivateTmp = true;
        ProtectSystem = "strict";
        ProtectHome = true;
        ReadWritePaths = [ cfg.stateDir ];
      };

      environment = {
        NARWAL_HTTP_HOST = cfg.listenAddress;
        NARWAL_HTTP_PORT = toString cfg.port;
        NARWAL_POSTGRES_URL = "postgresql://${cfg.database.user}@${cfg.database.host}:${toString cfg.database.port}/${cfg.database.name}?sslmode=disable";

        # NARWAL_S3_BUCKET_NAME = cfg.s3.bucketName;
        # NARWAL_S3_BUCKET_ENDPOINT = cfg.s3.endpoint;
        # NARWAL_S3_ACCESS_KEY_ID = cfg.s3.accessKeyId;
        # # FIXME: make this secret
        # NARWAL_S3_SECRET_ACCESS_KEY = cfg.s3.secretAccessKey;
        # TODO: NARWAL_STATE_DIR = cfg.stateDir;
      };

      script = ''
        exec ${cfg.package}/bin/narwal server
      '';
    };

    services.nginx = mkIf cfg.nginx.enable {
      enable = true;
      virtualHosts.${cfg.nginx.serverName} = {
        locations."/" = {
          proxyPass = "http://${cfg.listenAddress}:${toString cfg.port}";
          extraConfig = ''
            proxy_set_header Host $host;
            proxy_set_header X-Real-IP $remote_addr;
            proxy_set_header X-Forwarded-For $proxy_add_x_forwarded_for;
            proxy_set_header X-Forwarded-Proto $scheme;
          '';
        };
      };
    };

    services.postgresql = {
      enable = mkDefault true;
      ensureDatabases = [ cfg.database.name ];
      ensureUsers = [
        {
          name = cfg.database.user;
          ensureDBOwnership = true;
        }
      ];
    };
  };
}
