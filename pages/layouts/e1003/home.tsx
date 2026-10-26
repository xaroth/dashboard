import { HomeScreen, homeData } from "../../components/HomeScreen";
import type { PageProps } from "../../lib/page";

export const data = homeData;

// Laid out like the E1001's home, 800 px wide, and scaled up to the panel.
export const scaled = true;

/** The main page on the E1003: HomeScreen without the skill queues. */
export default function Home(props: PageProps<typeof data>) {
  return <HomeScreen {...props} />;
}
