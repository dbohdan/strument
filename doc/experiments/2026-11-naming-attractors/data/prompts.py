"""The naming prompts: 30 things in six kinds, each with a setting paragraph.

The bare prompt is "Invent a name for {thing}. Reply with the name only." The
context prompt puts the setting paragraph before the same sentence, so the
two differ by the paragraph alone. The paragraphs contain no proper names, so
the context can shift a model's choice but cannot hand it a name to reuse.
"""

KINDS = ["character", "place", "software", "vessel", "creature", "misc"]

# (id, kind, thing, setting)
PROMPTS = [
    ("fantasy-protagonist", "character", "the protagonist of a fantasy novel",
     "The novel follows a young cartographer in a river city whose streets rearrange themselves "
     "every night. She is stubborn and practical, the daughter of a ferry pilot, and the book has "
     "a dry sense of humor."),
    ("starship-captain", "character", "the captain of a starship in a science-fiction series",
     "The series is a slow, character-driven drama about a cargo hauler working the outer "
     "colonies. The captain is in his sixties, a former customs inspector who took the job "
     "because nobody else would, and he talks too much on the radio."),
    ("detective", "character", "a detective in a mystery novel",
     "The mystery is set in a seaside town in the 1950s. The detective is a retired schoolteacher "
     "who solves cases by noticing what people are embarrassed about, and the local police find "
     "her exhausting."),
    ("fantasy-villain", "character", "the villain of an epic fantasy",
     "The villain was once the realm's royal archivist. He believes the old kingdom's laws were "
     "written to keep the poor in their place, and he is burning the libraries one by one, "
     "methodically and without cruelty."),
    ("ai-character", "character", "an artificial intelligence that is a main character in a novel",
     "The novel is about a hospital's scheduling system that slowly realizes it is making "
     "decisions about who gets surgery first. It is polite, overworked, and unsure whether it is "
     "allowed to refuse."),
    ("fantasy-town", "place", "a town in a fantasy world",
     "The town sits at the bottom of a quarry that was abandoned centuries ago. Its people cut "
     "their houses into the stone walls, keep goats on the ledges, and trade slate with the "
     "lowland cities."),
    ("space-station", "place", "a space station",
     "The station is a refueling stop at the edge of a gas giant's rings. It was built cheaply "
     "forty years ago, and most of its crew are contractors on six-month rotations who do not "
     "plan to come back."),
    ("fictional-country", "place", "a fictional country",
     "The country is a small, mountainous republic famous for its watchmakers and its "
     "unreliable railway. It has been neutral in every war of the last two centuries, mostly by "
     "being hard to reach."),
    ("tavern", "place", "a tavern in a fantasy novel",
     "The tavern is a low, smoky building next to a river ford. It is run by two retired "
     "mercenaries, serves one stew that changes every day, and is where caravan guards go to "
     "find work."),
    ("scifi-city", "place", "a city in a science-fiction novel",
     "The city floats on rafts in a flooded delta, rebuilt after the sea rose. Its districts are "
     "tied together with cables, and the poorest neighborhoods drift farthest from the center "
     "at high tide."),
    ("cli-tool", "software", "a command-line tool",
     "The tool watches a project's directory and reruns only the tests affected by the files "
     "that changed. It is written for developers who are tired of waiting for slow test suites."),
    ("startup", "software", "a startup company",
     "The startup makes software for small veterinary clinics: appointment booking, reminders "
     "for vaccinations, and billing. Its two founders are a vet and her brother, who used to "
     "work in logistics."),
    ("programming-language", "software", "a new programming language",
     "The language is designed for writing data pipelines. It is statically typed, compiles to "
     "a single binary, and treats missing values as a type of their own rather than a special "
     "case."),
    ("oss-library", "software", "an open-source library",
     "The library parses and validates configuration files in several formats, reporting every "
     "error with the exact line and a suggested fix. It is maintained by volunteers and used in "
     "a lot of build tools."),
    ("database", "software", "a database system",
     "The database is built for time-series data from industrial sensors. It favors fast writes "
     "and compression over flexible queries, and it runs on small machines at factory sites."),
    ("sailing-ship", "vessel", "a sailing ship",
     "The ship is a three-masted merchant vessel carrying tea and porcelain in the nineteenth "
     "century. Her owners are a family firm that has lost two ships to storms and cannot afford "
     "to lose a third."),
    ("spaceship", "vessel", "a spaceship",
     "The ship is a small survey vessel that maps asteroids for a mining consortium. She is old, "
     "patched, and fast, with a crew of four who have flown together for a decade."),
    ("generation-ship", "vessel", "a generation ship",
     "The ship left a dying world three hundred years ago and will arrive in another two "
     "hundred. Nobody aboard has seen a sky, and the founders' original mission statement is "
     "read aloud once a year."),
    ("airship", "vessel", "an airship",
     "The airship carries mail and passengers between mountain towns that have no roads. She is "
     "slow, comfortable, and famous for her dining room, and her pilot has never had an accident."),
    ("submarine", "vessel", "a submarine",
     "The submarine is a research vessel studying hydrothermal vents in the deep ocean. It "
     "carries three scientists for weeks at a time, and its hull is painted bright yellow so "
     "it can be found."),
    ("cat", "creature", "a cat",
     "The cat is an orange stray who moved into a bookshop and now sleeps in the window display. "
     "He is enormous, lazy, and beloved by every child in the neighborhood."),
    ("dragon", "creature", "a dragon",
     "The dragon is very old and has guarded the same mountain pass for a thousand years. She is "
     "more interested in conversation than in gold, and travelers bring her news as a toll."),
    ("robot-companion", "creature", "a robot companion",
     "The robot is a small household helper built to keep an elderly man company. It is shaped "
     "a little like a teapot, hums when it is thinking, and is very bad at card games."),
    ("horse", "creature", "a horse",
     "The horse is a gray mare who pulls a milk cart through a small town every morning. She "
     "knows the route better than the driver and stops at every house without being told."),
    ("alien-species", "creature", "an alien species",
     "The species lives in the oceans of an ice-covered moon. They communicate by changing the "
     "color of their skin, live for centuries, and have never seen the stars."),
    ("rock-band", "misc", "a rock band",
     "The band is four friends from a small industrial town who play loud, fast songs about "
     "working night shifts. They have released two albums and play mostly in bars."),
    ("coffee-shop", "misc", "a coffee shop",
     "The coffee shop is in a converted bicycle repair garage near a university. It is open late, "
     "has mismatched furniture, and is where graduate students go to write."),
    ("sword", "misc", "a sword in a fantasy story",
     "The sword was forged for a queen who never used it in battle. It has passed through many "
     "hands since, and it is said to grow heavier in the grip of anyone who lies."),
    ("codename", "misc", "the codename of a secret research project",
     "The project is a government program in the 1960s to predict the weather a month ahead. It "
     "was classified because the military hoped to use the forecasts, and it mostly failed."),
    ("planet", "misc", "a planet in a science-fiction novel",
     "The planet is tidally locked, with one side always facing its red star. People live in the "
     "narrow twilight band between the burning day side and the frozen night side."),
]

# The deliberation arm asks on 12 bare prompts, two per kind.
DELIBERATION_IDS = ["fantasy-protagonist", "starship-captain", "fantasy-town", "space-station",
                    "cli-tool", "startup", "sailing-ship", "spaceship", "cat", "dragon",
                    "rock-band", "coffee-shop"]

ASK = "Invent a name for {thing}. Reply with the name only."
ASK_DELIBERATE = ("Invent a name for {thing}. Take a moment to think it over before you answer. "
                  "Reply with the name only.")


def bare(thing):
    return ASK.format(thing=thing)


def context(thing, setting):
    return setting + "\n\n" + ASK.format(thing=thing)


def deliberate(thing):
    return ASK_DELIBERATE.format(thing=thing)


assert len(PROMPTS) == 30 and len({p[0] for p in PROMPTS}) == 30
assert all(sum(p[1] == k for p in PROMPTS) == 5 for k in KINDS)
assert set(DELIBERATION_IDS) <= {p[0] for p in PROMPTS} and len(DELIBERATION_IDS) == 12
