import { HomeScreen, homeData, homeSkills } from "../../components/HomeScreen";
import type { PageProps } from "../../lib/page";

export const data = { ...homeData, ...homeSkills };

/** The main page on the E1001: HomeScreen with the skill queues. */
export default function Home(props: PageProps<typeof data>) {
  return <HomeScreen {...props} />;
}
