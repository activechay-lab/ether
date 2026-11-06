// Hosted runtime: requests pass through Ether's network allowlist.
function get(url) {
  const response = ether.http.fetch({ method: "GET", url: url });
  if (response.status !== 200) throw new Error("Weather service unavailable");
  return JSON.parse(response.body);
}
function condition(code, night) {
  if (code === 0)
    return [night ? "Clear night" : "Clear skies", night ? "moon" : "sun"];
  if (code <= 2) return ["Partly cloudy", "partly"];
  if (code === 3) return ["Overcast", "cloud"];
  if (code <= 48) return ["Fog", "fog"];
  if (code >= 95) return ["Thunderstorms", "storm"];
  if ((code >= 71 && code <= 77) || code === 85 || code === 86)
    return ["Snow", "snow"];
  if (code === 56 || code === 57 || code === 66 || code === 67)
    return ["Freezing rain", "rain"];
  return ["Rain", "rain"];
}
function place(value) {
  const p = typeof value === "string" ? JSON.parse(value) : value;
  if (
    !p ||
    !p.name ||
    !Number.isFinite(p.latitude) ||
    !Number.isFinite(p.longitude) ||
    Math.abs(p.latitude) > 90 ||
    Math.abs(p.longitude) > 180
  )
    throw new Error("Choose a location in Weather settings");
  return p;
}
globalThis.handle = function (req) {
  if (req.path === "/ether/options/location") {
    const q = String((req.query && req.query.q) || "").trim();
    if (q.length < 2) return { status: 200, body: [] };
    const data = get(
      "https://geocoding-api.open-meteo.com/v1/search?count=8&name=" +
        encodeURIComponent(q) +
        "&language=" +
        encodeURIComponent(String(req.query.locale || "en").split("-")[0]),
    );
    return {
      status: 200,
      body: (data.results || []).map(function (p) {
        const name = [p.name, p.admin1, p.country]
          .filter(Boolean)
          .filter(function (v, i, a) {
            return a.indexOf(v) === i;
          })
          .join(", ");
        return {
          label: name,
          value: JSON.stringify({
            name: name,
            latitude: p.latitude,
            longitude: p.longitude,
          }),
        };
      }),
    };
  }
  if (req.op === "feed" && (req.id === "now" || req.id === "daily")) {
    const settings = ether.settings.get();
    const p = place(settings.location);
    const imperial =
      settings.units === "imperial" ||
      (settings.units === "locale" &&
        /[-_](US|LR|MM)([.@]|$)/i.test(
          (req.headers || {})["X-Ether-Locale"] || "",
        ));
    const degree = imperial ? "°F" : "°C";
    const base =
      "https://api.open-meteo.com/v1/forecast?latitude=" +
      p.latitude +
      "&longitude=" +
      p.longitude +
      "&timezone=auto&forecast_days=5" +
      (imperial
        ? "&temperature_unit=fahrenheit&wind_speed_unit=mph&precipitation_unit=inch"
        : "");
    if (req.id === "now") {
      const data = get(
        base +
          "&current=temperature_2m,apparent_temperature,relative_humidity_2m,wind_speed_10m,wind_direction_10m,weather_code,is_day",
      );
      const c = data.current;
      if (
        !c ||
        [
          c.temperature_2m,
          c.apparent_temperature,
          c.relative_humidity_2m,
          c.wind_speed_10m,
          c.wind_direction_10m,
          c.weather_code,
          c.is_day,
        ].some(function (v) {
          return v == null || !Number.isFinite(v);
        })
      )
        throw new Error("Incomplete current conditions");
      const sky = condition(c.weather_code, !c.is_day);
      return {
        status: 200,
        body: {
          place: p.name,
          temp: Math.round(c.temperature_2m) + degree,
          feels: Math.round(c.apparent_temperature) + degree,
          humidity: c.relative_humidity_2m + "%",
          wind:
            ["N", "NE", "E", "SE", "S", "SW", "W", "NW"][
              Math.round(c.wind_direction_10m / 45) % 8
            ] +
            " " +
            Math.round(c.wind_speed_10m) +
            (imperial ? " mph" : " km/h"),
          summary: sky[0],
          icon: "assets/" + sky[1] + ".png",
          updated: c.time.slice(11, 16) + " " + data.timezone_abbreviation,
          ticker:
            p.name +
            "   /   " +
            sky[0] +
            "   /   Feels like " +
            Math.round(c.apparent_temperature) +
            degree +
            "   /   Wind " +
            Math.round(c.wind_speed_10m) +
            (imperial ? " mph" : " km/h") +
            "   /   Humidity " +
            c.relative_humidity_2m +
            "%",
        },
      };
    }
    const data = get(
      base +
        "&daily=weathercode,temperature_2m_max,temperature_2m_min,precipitation_probability_max,precipitation_sum,snowfall_sum",
    );
    const d = data.daily,
      out = {};
    if (!d || !d.time || d.time.length < 5)
      throw new Error("Incomplete daily forecast");
    for (let i = 0; i < 5; i++) {
      if (
        [
          d.weathercode[i],
          d.temperature_2m_max[i],
          d.temperature_2m_min[i],
          d.precipitation_probability_max[i],
          d.precipitation_sum[i],
          d.snowfall_sum[i],
        ].some(function (v) {
          return v == null || !Number.isFinite(v);
        })
      )
        throw new Error("Incomplete forecast day");
      const sky = condition(d.weathercode[i], false),
        k = "d" + i;
      out[k + "name"] =
        i === 0
          ? "TODAY"
          : i === 1
            ? "TOMORROW"
            : [
                "SUNDAY",
                "MONDAY",
                "TUESDAY",
                "WEDNESDAY",
                "THURSDAY",
                "FRIDAY",
                "SATURDAY",
              ][new Date(d.time[i] + "T12:00:00Z").getUTCDay()];
      out[k + "high"] = Math.round(d.temperature_2m_max[i]) + degree;
      out[k + "low"] = Math.round(d.temperature_2m_min[i]) + degree;
      out[k + "summary"] = sky[0];
      out[k + "icon"] = "assets/" + sky[1] + ".png";
      out[k + "rain"] =
        d.precipitation_probability_max[i] +
        "%  /  " +
        d.precipitation_sum[i].toFixed(1) +
        (imperial ? " in" : " mm");
      out[k + "snow"] =
        "Snow " + d.snowfall_sum[i].toFixed(1) + (imperial ? " in" : " cm");
    }
    return { status: 200, body: out };
  }
  if (req.path === "/ether/configure" || req.path === "/ether/test") {
    try {
      place(ether.settings.get().location);
    } catch (e) {
      return {
        status: 200,
        body: { ok: false, errors: { location: String(e.message) } },
      };
    }
    return { status: 200, body: { ok: true } };
  }
  if (req.path === "/ether/health")
    return { status: 200, body: { status: "ok" } };
  return { status: 200, body: { ok: true } };
};
