// Known same-origin stores used by Go and legacy Python dictation. No catch-all
// browser clearing: credentials, ordinary text drafts, and unrelated app data stay.
export const DICTATION_DATABASES = ["tmx-dictations", "tmx-voice"] as const;
export const DICTATION_WEB_KEYS = ["tmx-voice-discarded", "tmx-voice-busy"] as const;

function clearDatabase(factory: IDBFactory, name: string): Promise<void> {
  return new Promise((resolve, reject) => {
    let absent = false;
    const request = factory.open(name);
    request.onupgradeneeded = event => {
      if (event.oldVersion === 0) { absent = true; request.transaction?.abort(); }
    };
    request.onerror = () => absent ? resolve() : reject(request.error);
    request.onblocked = () => reject(new Error("Dictation cleanup is waiting for another window."));
    request.onsuccess = () => {
      const db = request.result;
      const stores = Array.from(db.objectStoreNames);
      if (!stores.length) { db.close(); resolve(); return; }
      try {
        const tx = db.transaction(stores, "readwrite");
        for (const store of stores) tx.objectStore(store).clear();
        tx.oncomplete = () => { db.close(); resolve(); };
        tx.onabort = tx.onerror = () => { db.close(); reject(tx.error); };
      } catch (error) { db.close(); reject(error); }
    };
  });
}

/** Best-effort legacy cleanup. New memory-only work never waits for this. */
export async function migrateDictationStores(env: {
  indexedDB?: IDBFactory; localStorage?: Storage; sessionStorage?: Storage;
}, resetMemory: () => void | Promise<void>, warn: (message: string) => void = console.warn) {
  const attempt = async (run: () => void | Promise<void>) => {
    try { await run(); } catch { warn("Legacy dictation cleanup failed; new dictation remains available."); }
  };
  await attempt(resetMemory);
  await Promise.all([
    ...DICTATION_DATABASES.map(name => attempt(async () => {
      if (env.indexedDB) await clearDatabase(env.indexedDB, name);
    })),
    ...[env.localStorage, env.sessionStorage].flatMap(storage =>
      storage ? DICTATION_WEB_KEYS.map(key => attempt(() => storage.removeItem(key))) : []),
  ]);
}
