#!/usr/bin/env python3
"""Build a tiny-file Plex library tree for the PMS baseline.

    python3 -I makelib.py BASE_DIR OUT_DIR TITLES

TITLES is the number of library titles: 70% movies, 30% shows. Shows get a
realistic spread of seasons (1-10, mean about 3) and episodes per season
(6-13, mean about 10). Episodes, posters and fanart are hard links to the
few-second sample files in BASE_DIR; each movie is its own MP4 so it can
carry embedded tags (title, year, genres, summary), which PMS reads offline
with the library's "prefer local metadata" (respectTags) setting. Movies are
written by tag-movies.sh from OUT_DIR/movies.tsv.

The output is deterministic for a given TITLES.
"""
import os
import random
import sys

WORDS = ["Star", "Night", "River", "Empire", "Shadow", "Garden", "Signal", "Harbor", "Winter", "Glass",
         "Echo", "Stone", "Summer", "Silver", "Iron", "Paper", "Ghost", "Ocean", "Crown", "Wild",
         "Falcon", "Ember", "Lantern", "Meridian", "Orchard", "Copper", "Velvet", "Thunder", "Atlas", "Willow"]
GENRES = ["Action", "Adventure", "Animation", "Comedy", "Crime", "Documentary", "Drama", "Family", "Fantasy",
          "History", "Horror", "Music", "Mystery", "Romance", "Science Fiction", "Thriller", "War", "Western"]
MOVIE_VARIANTS = [("movie-1080p-h264.mp4", 60), ("movie-2160p-hevc.mp4", 20), ("movie-720p-h264.mp4", 20)]
EPISODE_VARIANTS = [("episode-1080p-h264.mkv", 70), ("episode-720p-h264.mp4", 30)]
SEASONS = [(1, 30), (2, 20), (3, 15), (4, 10), (5, 8), (6, 6), (7, 4), (8, 3), (9, 2), (10, 2)]


def weighted(rng, choices):
    return rng.choices([c for c, _ in choices], weights=[w for _, w in choices])[0]


def unique_titles(rng, n, used):
    out = []
    while len(out) < n:
        k = rng.choice([1, 2, 2, 3])
        title = " ".join(rng.sample(WORDS, k))
        if rng.random() < 0.25:
            title = "The " + title
        year = rng.randint(1950, 2026)
        if (title, year) in used:
            continue
        used.add((title, year))
        out.append((title, year))
    return out


def link(src, dst):
    if not os.path.exists(dst):
        os.link(src, dst)


def main():
    base, out, titles = sys.argv[1], sys.argv[2], int(sys.argv[3])
    rng = random.Random(titles)
    movies, shows = titles * 7 // 10, titles - titles * 7 // 10
    used = set()
    os.makedirs(os.path.join(out, "movies"), exist_ok=True)
    os.makedirs(os.path.join(out, "shows"), exist_ok=True)
    poster, fanart = os.path.join(base, "poster.jpg"), os.path.join(base, "fanart.jpg")
    with open(os.path.join(out, "movies.tsv"), "w") as tsv:
        for title, year in unique_titles(rng, movies, used):
            name = f"{title} ({year})"
            d = os.path.join(out, "movies", name)
            os.makedirs(d, exist_ok=True)
            link(poster, os.path.join(d, "poster.jpg"))
            link(fanart, os.path.join(d, "fanart.jpg"))
            genres = ", ".join(rng.sample(GENRES, rng.choice([1, 2, 2, 3])))
            summary = f"{title} follows {rng.choice(WORDS).lower()} and {rng.choice(WORDS).lower()} across {year}."
            tsv.write("\t".join([weighted(rng, MOVIE_VARIANTS), f"movies/{name}/{name}.mp4", title, str(year), genres, summary]) + "\n")
    episodes = seasons_total = 0
    for title, year in unique_titles(rng, shows, used):
        name = f"{title} ({year})"
        d = os.path.join(out, "shows", name)
        os.makedirs(d, exist_ok=True)
        link(poster, os.path.join(d, "poster.jpg"))
        link(fanart, os.path.join(d, "fanart.jpg"))
        variant = weighted(rng, EPISODE_VARIANTS)
        ext = os.path.splitext(variant)[1]
        for s in range(1, weighted(rng, SEASONS) + 1):
            sd = os.path.join(d, f"Season {s:02d}")
            os.makedirs(sd, exist_ok=True)
            seasons_total += 1
            for e in range(1, rng.randint(6, 13) + 1):
                link(os.path.join(base, variant), os.path.join(sd, f"{name} - s{s:02d}e{e:02d}{ext}"))
                episodes += 1
    print(f"{movies} movies, {shows} shows, {seasons_total} seasons, {episodes} episodes")


if __name__ == "__main__":
    main()
