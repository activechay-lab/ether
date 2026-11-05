// Hosted runtime: all network calls pass through Ether's declared host allowlist.
globalThis.handle = function (req) {
  if (req.op === "feed" && req.id === "now") {
    const settings = ether.settings.get();
    const lat = Number(settings.latitude == null ? 43.65 : settings.latitude);
    const lon = Number(
      settings.longitude == null ? -79.38 : settings.longitude,
    );
    const result = ether.http.fetch({
      method: "GET",
      url:
        "https://api.open-meteo.com/v1/forecast?latitude=" +
        lat +
        "&longitude=" +
        lon +
        "&current=temperature_2m",
    });
    if (result.status !== 200) throw new Error("Weather fetch failed");
    const data = JSON.parse(result.body);
    return {
      status: 200,
      body: {
        temp: data.current.temperature_2m,
        summary: "Current temperature",
      },
    };
  }
  if (req.path === "/ether/health")
    return { status: 200, body: { status: "ok" } };
  return { status: 200, body: { ok: true } };
};
