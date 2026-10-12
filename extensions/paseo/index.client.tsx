import type { PluginClientContext, PluginSidebarItemProps } from "@getpaseo/plugin/client";
import { SidebarRow } from "@getpaseo/plugin/client/ui";
import { SquadScreen } from "./client/screen";
import { SquadSettings } from "./client/settings";

function SquadItem({ currentScreen, openScreen }: PluginSidebarItemProps) {
  return <SidebarRow icon="Users" active={currentScreen?.screenId === "squad"} onPress={() => openScreen({ screenId: "squad" })} />;
}

export default function contribute(client: PluginClientContext) {
  const cleanups = [
    client.addScreen({ id: "squad", title: "Squad", Component: SquadScreen }),
    client.addSidebarHeaderItem({ id: "squad", title: "Squad", Component: SquadItem }),
    client.addSettingsScreen({ id: "squad", title: "Squad", icon: "Users", Component: SquadSettings }),
  ];
  return () => cleanups.forEach((cleanup) => cleanup());
}
