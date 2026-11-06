# Local weather for On Air

Optional hosted example (`community.example.weather`); Ether never installs it
by default. Pack with `ether plugin pack -o /tmp/weather.etherpkg examples/onair-weather`.
In **Plugins › Weather › Settings**, search a city or postal code, choose the
resolved place, choose units, then save. The chosen string stores the place's
display name, latitude and longitude together; no Toronto location is assumed.
Units follow the server process locale by default (US/LR/MM use imperial), with
explicit metric and imperial overrides. An unset/C locale uses metric.

Place **Local forecast** on a channel's clock or as a plugin programming source.
The Plugin settings link opens the same settings drawer. The 48-second segment
shows current conditions (0–16 s), today/tomorrow (16–32 s), then five days
(32–48 s), with sequenced wipes and a continuous crawl. Palette, display/body
fonts and icon tint inherit the channel style. With no style it uses Clean.

Both feeds refresh every 15 minutes. `now` expires after one hour and `daily`
after six hours. A failed/incomplete response retains the last good feed;
expired segments fall back without changing programme times. Samples are
illustrative preview data, not observations. Current update time is the
provider's observation time in the location's timezone. Daily precipitation is
total water equivalent; snowfall is also shown separately (cm or inches).

Icons in `assets/` are original geometric artwork authored for this example;
no external icon set is used. The scene displays Open-Meteo's CC BY 4.0 credit.
Weather API and geocoding network access are declared explicitly. See
[forecast docs](https://open-meteo.com/en/docs) and
[geocoding docs](https://open-meteo.com/en/docs/geocoding-api) for provider terms.

Offline example checks: `node --test examples/onair-weather/weather.test.cjs`.
