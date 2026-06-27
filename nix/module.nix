{ config, lib, pkgs, ... }:

let
  cfg = config.services.mesh-exporter;
  settingsFormat = pkgs.formats.yaml { };
  configFile = settingsFormat.generate "mesh-exporter.yaml" cfg.settings;
in
{
  options.services.mesh-exporter = {
    enable = lib.mkEnableOption "mesh-exporter Prometheus exporter for EasyTier and sing-box";

    package = lib.mkPackageOption pkgs "mesh-exporter" { };

    settings = lib.mkOption {
      type = settingsFormat.type;
      default = { };
      description = "mesh-exporter configuration (serialized to YAML)";
      example = lib.literalExpression ''
        {
          listen = ":9550";
          collectors = {
            easytier = {
              enabled = true;
              rpc_address = "127.0.0.1:15888";
            };
          };
        }
      '';
    };

    port = lib.mkOption {
      type = lib.types.port;
      default = 9550;
      description = "Port for the metrics HTTP server";
    };

    openFirewall = lib.mkOption {
      type = lib.types.bool;
      default = false;
      description = "Open the metrics port in the firewall";
    };
  };

  config = lib.mkIf cfg.enable {
    # Inject the listen address from the port option
    services.mesh-exporter.settings.listen = lib.mkDefault ":${toString cfg.port}";

    systemd.services.mesh-exporter = {
      description = "Prometheus exporter for EasyTier mesh and sing-box";
      wantedBy = [ "multi-user.target" ];
      after = [ "network.target" ];

      serviceConfig = {
        Type = "simple";
        ExecStart = "${lib.getExe cfg.package} -config ${configFile}";
        Restart = "always";
        RestartSec = 10;

        # Hardening
        DynamicUser = true;
        NoNewPrivileges = true;
        ProtectSystem = "strict";
        ProtectHome = true;
        PrivateTmp = true;
        PrivateDevices = true;
        ProtectKernelTunables = true;
        ProtectControlGroups = true;
        RestrictSUIDSGID = true;
        MemoryDenyWriteExecute = true;
      };
    };

    networking.firewall.allowedTCPPorts = lib.mkIf cfg.openFirewall [ cfg.port ];
  };
}
