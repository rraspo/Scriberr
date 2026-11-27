// Readable English name for a language tag such as "en-US" or "es"; falls
// back to the tag itself when the runtime cannot name it.
export function languageName(tag: string): string {
    try {
        const name = new Intl.DisplayNames(["en"], { type: "language" }).of(tag);
        return name && name !== tag ? name : tag;
    } catch {
        return tag;
    }
}
