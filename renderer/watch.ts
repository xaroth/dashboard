import { watch } from "node:fs";

/**
 * Runs rebuild after changes under dirs settle. One rebuild at a time, so an
 * older one never finishes after a newer one; a change during a rebuild runs
 * another after it.
 */
export function rebuildOnChange(dirs: string[], rebuild: () => Promise<void>) {
  let timer: Timer | undefined;
  let building = false;
  let again = false;
  const run = async () => {
    if (building) return void (again = true);
    building = true;
    try {
      await rebuild();
    } finally {
      building = false;
      if (again) {
        again = false;
        run();
      }
    }
  };
  for (const dir of dirs) {
    const watcher = watch(dir, { recursive: true }, () => {
      clearTimeout(timer);
      timer = setTimeout(run, 100);
    });
    // Without a listener a watch error is uncaught and ends the process; keep serving the current bundle.
    watcher.on("error", (e) => {
      console.error(`renderer: watching ${dir} failed, no more rebuilds: ${e}`);
      watcher.close();
    });
  }
}
