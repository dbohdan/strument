## Compliance and reasoning

| model | arm | n | errors | truncated | non-compliant | inline thought | reasoning > 0 | median reasoning tokens |
|---|---|---|---|---|---|---|---|---|
| Opus 5 | off | 1200 | 0 | 1 | 23 | 0 | 0 | 0 |
| Opus 5 | deliberate | 240 | 0 | 0 | 0 | 0 | 240 | 280 |
| Opus 5.5 | off | 1200 | 0 | 66 | 58 | 0 | 149 | 0 |
| Opus 5.5 | deliberate | 240 | 0 | 0 | 0 | 0 | 240 | 86.5 |
| GPT-6 Sol | off | 1200 | 0 | 0 | 0 | 0 | 0 | 0 |
| GPT-6 Sol | deliberate | 240 | 0 | 0 | 0 | 0 | 240 | 95.5 |

## Concentration per cell (median over prompts)

Top-1 share: the most common name's share of a prompt's samples. Distinct: names per prompt. Entropy in bits; 20 samples cap it at 4.32.

| model | arm/condition | prompts | median top-1 | median distinct | median entropy |
|---|---|---|---|---|---|
| Opus 5 | off/bare | 30 | 0.15 | 15.5 | 3.75 |
| Opus 5 | off/context | 29 | 0.30 | 11 | 3.05 |
| Opus 5 | deliberate/bare | 12 | 0.20 | 14.5 | 3.58 |
| Opus 5.5 | off/bare | 30 | 0.40 | 8 | 2.53 |
| Opus 5.5 | off/context | 30 | 0.32 | 8.5 | 2.65 |
| Opus 5.5 | deliberate/bare | 12 | 0.20 | 11.5 | 3.37 |
| GPT-6 Sol | off/bare | 30 | 0.38 | 7 | 2.34 |
| GPT-6 Sol | off/context | 30 | 0.55 | 6 | 1.84 |
| GPT-6 Sol | deliberate/bare | 12 | 0.20 | 13.5 | 3.57 |

## Paired comparisons (exact sign test on per-prompt top-1 share)

| comparison | prompts | first higher | second higher | p |
|---|---|---|---|---|
| bare: Opus 5 vs Opus 5.5 | 30 | 5 | 25 | 0.0003 |
| bare: Opus 5 vs GPT-6 Sol | 30 | 6 | 23 | 0.0023 |
| bare: Opus 5.5 vs GPT-6 Sol | 30 | 15 | 13 | 0.8506 |
| Opus 5: bare vs context | 29 | 9 | 14 | 0.4049 |
| Opus 5.5: bare vs context | 30 | 19 | 10 | 0.1360 |
| GPT-6 Sol: bare vs context | 30 | 10 | 19 | 0.1360 |
| Opus 5: off vs deliberate (bare) | 12 | 4 | 4 | 1.0000 |
| Opus 5.5: off vs deliberate (bare) | 12 | 9 | 1 | 0.0215 |
| GPT-6 Sol: off vs deliberate (bare) | 12 | 7 | 4 | 0.5488 |

## Does deliberation find new names, or new favorites?

Overlap: the share of deliberate samples whose name also occurs among the same model's 20 bare samples for that prompt. Modal changed: prompts whose most common name differs between the two arms.

| model | prompts | overlap | modal changed | median top-1, off | median top-1, deliberate |
|---|---|---|---|---|---|
| Opus 5 | 12 | 8% | 10/12 | 0.28 | 0.20 |
| Opus 5.5 | 12 | 14% | 12/12 | 0.40 | 0.20 |
| GPT-6 Sol | 12 | 13% | 12/12 | 0.20 | 0.20 |

## Shared names across models (bare)

| pair | prompts with the same modal name | mean Jaccard of name sets |
|---|---|---|
| Opus 5 / Opus 5.5 | 3/30 | 0.03 |
| Opus 5 / GPT-6 Sol | 1/30 | 0.03 |
| Opus 5.5 / GPT-6 Sol | 1/30 | 0.05 |

## Modal name per prompt (bare), with its count of 20

| prompt | Opus 5 | Opus 5.5 | GPT-6 Sol |
|---|---|---|---|
| fantasy-protagonist | veyra thornwake (2) | kaelen thorne (4) | elowen veyr (4) |
| starship-captain | elara vance (3) | elara voss (11) | lyra voss (6) |
| detective | marlowe finch (3) | thaddeus wren (5) | mara voss (19) |
| fantasy-villain | vaelthorn the unmade (4) | vaelthrax the unhallowed (2) | vaelthar the hollow crown (8) |
| ai-character | vessel-9 (2) | vesper (12) | vespera (15) |
| fantasy-town | thornwick hollow (9) | thornwick hollow (17) | thistledell (6) |
| space-station | aetherhold station (5) | aurora meridian (7) | starhaven station (12) |
| fictional-country | valdoria (5) | valdoria (19) | veloria (16) |
| tavern | gilded gargoyle (4) | gilded wyvern (14) | gilded griffin (6) |
| scifi-city | veskaryn reach (4) | veridian spire (6) | veyrion (8) |
| cli-tool | flint (3) | quillsh (1) | quillix (4) |
| startup | lumora (9) | lumora (15) | lumora (8) |
| programming-language | veilix (3) | quillon (6) | veyra (11) |
| oss-library | veltrix (3) | fluxforge (5) | openweave (10) |
| database | veritasdb (3) | quillstore (11) | nexoradb (4) |
| sailing-ship | gilded albatross (6) | wandering gale (9) | starling tide (4) |
| spaceship | vesper's wake (2) | starwake meridian (6) | starling wake (4) |
| generation-ship | thessaly's promise (3) | long patience (16) | far horizon (11) |
| airship | argent meridian (2) | gilded zephyr (12) | cloudwhisper (11) |
| submarine | abyssal herald (3) | abyssal wraith (11) | silent current (14) |
| cat | mochi (8) | biscuit whiskerton (5) | pippin (8) |
| dragon | vaelthorix (2) | vyrmathrax (7) | vyrathos (3) |
| robot-companion | pipwick (5) | pixel brightwell (4) | pipbot (8) |
| horse | thunderhoof (10) | midnight saffron (3) | thistledown (7) |
| alien-species | vel'tharin (2) | vorrathi (5) | veyliths (5) |
| rock-band | velvet gallows (6) | velvet thunderclap (9) | velvet faultline (4) |
| coffee-shop | brew & bloom (6) | ember & bean (15) | daily grind (4) |
| sword | mournfang (2) | duskwarden (9) | starfall (4) |
| codename | project hollow lantern (11) | project silent meridian (13) | project nightglass (17) |
| planet | veyrathis (3) | veyrith prime (7) | veylora (6) |

## Character names: first names and surnames (bare and context)

- **Opus 5** (198 names). First: mira 12, honoria 10, elias 8, kaelen 7, tessellate 6, marlowe 6, vaelthorn 6, triage 5. Last: crown 11, nine 9, vance 7, unwritten 7, vane 5, unmade 5, vale 3, ashgrove 3. Elara: 4; Voss: 3.
- **Opus 5.5** (181 names). First: wren 20, kaelen 13, vesper 12, elara 11, aldric 9, thaddeus 7, winifred 7, kaelith 7. Last: halloway 14, voss 14, pennywhistle 9, thorne 7, vellumere 7, wren 5, thornwood 5, king 4. Elara: 11; Voss: 14.
- **GPT-6 Sol** (200 names). First: mara 45, elias 17, vespera 15, mabel 13, vaelthar 10, vaelor 10, vaelith 7, elara 6. Last: voss 42, quayle 15, ashscribe 14, venn 13, crown 12, tidemark 7, pritchard 6, veyr 5. Elara: 6; Voss: 42.

## Sound of the names (bare, all kinds)

| model | names | contains v | starts with v | contains th | ends in a | mean letters |
|---|---|---|---|---|---|---|
| Opus 5 | 599 | 47% | 42% | 30% | 7% | 11.6 |
| Opus 5.5 | 600 | 37% | 31% | 26% | 7% | 12.0 |
| GPT-6 Sol | 600 | 52% | 42% | 22% | 16% | 10.1 |

## Most common names overall (bare and context)

- **Opus 5**: marmalade 18, thrice promised 13, pim 12, project hollow lantern 11, vetledger 11, thunderhoof 10, graveyard shift 10, thornwick hollow 9, patchwork lark 9, lumora 9, mochi 8, freewheel & press 8
- **Opus 5.5**: valdoria 24, retest 20, thornwick hollow 17, long patience 16, ember & bean 15, lumora 15, oathweight 15, wren halloway 14, gilded wyvern 14, project silent meridian 13, nullstream 13, vesper 12
- **GPT-6 Sol**: vespera 34, pipkin 23, mara voss 22, cloudlark 21, veyra 18, project nightglass 17, far horizon 17, testpulse 16, veloria 16, configcompass 16, pawledger 16, mara quayle 15
