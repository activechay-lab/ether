# Weather segment example

This optional plugin fetches household temperature from [Open-Meteo](https://open-meteo.com/en/docs).
It is an example, never installed or enabled by Ether. Check the API's current
terms before using it commercially. Weather data requires attribution (CC BY 4.0),
which the scene displays.

Pack with `ether plugin pack -o /tmp/weather.etherpkg examples/onair-weather`, then
install the package in Plugins. Choose latitude and longitude at install. On a
virtual channel, add **On the clock**, choose **Local forecast**, and save. The
same segment can be selected as break content in Look. It inherits the selected
look's palette and fonts. With no look it uses Clean.

Feeds refresh on install (the next background host tick) and every 15 minutes.
After one hour without a successful fetch the slot falls back without moving
programme times. The unrated segment requires Allow unrated plugin segments on
a channel with a rating cap, and remains subject to each viewer's policy.

Packaged images use package-relative paths; inline images use base64 bytes.
Scene moments must match `plugin:<manifest id>/<segment id>`.
