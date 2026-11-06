const { test } = require("node:test");
const assert = require("node:assert/strict");
const fs = require("node:fs");
const vm = require("node:vm");
const manifest = require("./manifest.json");
const location = JSON.stringify({
  name: "Toronto, Ontario, Canada",
  latitude: 43.65,
  longitude: -79.38,
});
function runtime(settings, fetch) {
  const context = vm.createContext({
    ether: { settings: { get: () => settings }, http: { fetch } },
    console,
  });
  vm.runInContext(fs.readFileSync(__dirname + "/index.js", "utf8"), context);
  return context.handle;
}
test("geocoding returns selectable, stored coordinate/name values", () => {
  const handle = runtime({}, ({ url }) => {
    assert.match(url, /name=New%20York/);
    return {
      status: 200,
      body: JSON.stringify({
        results: [
          {
            name: "New York",
            admin1: "New York",
            country: "United States",
            latitude: 40.7,
            longitude: -74,
          },
        ],
      }),
    };
  });
  const result = handle({
    path: "/ether/options/location",
    query: { q: ["New York"], locale: ["en-US"] },
  });
  const place = JSON.parse(result.body[0].value);
  assert.equal(place.name, "New York, United States");
  assert.equal(place.latitude, 40.7);
});
test("locale defaults, explicit units and null rejection", () => {
  for (const [units, locale, imperial] of [
    ["locale", "en-US", true],
    ["metric", "en-US", false],
    ["imperial", "fr-CA", true],
  ]) {
    const handle = runtime({ location, units }, ({ url }) => {
      assert.equal(url.includes("temperature_unit=fahrenheit"), imperial);
      return {
        status: 200,
        body: JSON.stringify({
          timezone_abbreviation: "EDT",
          current: {
            time: "2026-11-06T16:15",
            temperature_2m: 18,
            apparent_temperature: 17,
            relative_humidity_2m: 64,
            wind_speed_10m: 14,
            wind_direction_10m: 225,
            weather_code: 2,
            is_day: 1,
          },
        }),
      };
    });
    const body = handle({
      op: "feed",
      id: "now",
      headers: { "X-Ether-Locale": locale },
    }).body;
    assert.match(body.temp, imperial ? /°F/ : /°C/);
    assert.equal(body.summary, "Partly cloudy");
    assert.deepEqual(
      Object.keys(body).sort(),
      Object.keys(manifest.ether.contributes.onairFeeds[0].fields).sort(),
    );
  }
  assert.throws(
    () =>
      runtime({ location, units: "metric" }, () => ({
        status: 200,
        body: '{"current":{"temperature_2m":null}}',
      }))({ op: "feed", id: "now" }),
    /Incomplete/,
  );
});
test("daily feed has five complete declared days and refuses missing values", () => {
  const daily = {
    time: [
      "2026-11-06",
      "2026-11-07",
      "2026-11-08",
      "2026-11-09",
      "2026-11-10",
    ],
  };
  for (const k of [
    "weathercode",
    "temperature_2m_max",
    "temperature_2m_min",
    "precipitation_probability_max",
    "precipitation_sum",
    "snowfall_sum",
  ])
    daily[k] = [1, 2, 3, 4, 5];
  const handle = runtime({ location, units: "metric" }, ({ url }) => {
    assert.match(url, /daily=weathercode/);
    return { status: 200, body: JSON.stringify({ daily }) };
  });
  const body = handle({ op: "feed", id: "daily" }).body;
  assert.deepEqual(
    Object.keys(body).sort(),
    Object.keys(manifest.ether.contributes.onairFeeds[1].fields).sort(),
  );
  daily.temperature_2m_min[3] = null;
  assert.throws(() => handle({ op: "feed", id: "daily" }), /Incomplete/);
});
