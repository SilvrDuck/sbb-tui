#!/usr/bin/env python3
"""Enrich the slim stations JSON with multilingual aliases derived from
Wikidata's city labels, station nicknames, IATA codes, and a small
universal transit-class word dictionary (Flughafen ↔ Aéroport ↔ ...).

Aliases are only emitted for stations with mode == "TRAIN"; other modes
(bus, tram, boat) very rarely benefit and would flood the popover with
near-duplicates.

Run:
    python3 scripts/merge_wikidata_aliases.py \\
        data/stations.json data/wikidata-aliases.json data/stations.json
"""

import json
import re
import sys

# Universal Romance/Germanic prepositions and articles that can appear
# between a transit-class word and the station name in altLabels
# (e.g. "Gare DE Cornavin", "Stazione DI Bellinzona", "Gare DE L'aéroport de
# Zurich"). After the transit-vocab strip lops off the wrapper word,
# these are stripped too. Universal grammar set, not transit-specific
# and not station-specific. Each is also present in Wikidata as its own
# entity but enumerating them programmatically is more brittle than this
# short hard list.
#
# Two flavours: word-prepositions need trailing whitespace ("de "),
# apostrophe-prepositions don't ("l'aéroport"). Both are matched
# case-insensitively.
PREPOSITIONS_WORD = (
    "de", "du", "des", "de la",
    "di", "del", "della", "dei", "delle",
    "of", "the",
    "von", "vom", "der", "die", "das",
    "le", "la", "les", "il", "lo", "gli", "i",
    "dal", "dals",
)
PREPOSITIONS_APOSTROPHE = ("d'", "dell'", "l'")


# Wrapper-vocabulary + substitution groups come from Wikidata, loaded
# from data/wikidata-vocab.json at runtime. See:
#   - WrapperVocab.build()     — flat strip-phrase set
#   - WrapperVocab.groups()    — substitution groups by concept


class WrapperVocab:
    """Holds the universal transit vocabulary fetched from Wikidata —
    labels and altLabels of railway station / central station / airport
    / metro station / tram stop. Drives both altLabel stripping and the
    transit-class word substitution. No hand-curated transit words."""

    def __init__(self, vocab_path: str) -> None:
        with open(vocab_path, encoding="utf-8") as f:
            self._raw = json.load(f)

        # Flat set of strip phrases. Each Wikidata phrase contributes
        # itself AND its first word — Q55488(it).label is "stazione
        # ferroviaria", but to strip "stazione di X" we also need the
        # bare "stazione" in the vocabulary. Longest-match-first sort
        # ensures the full phrase wins when it actually matches.
        phrases: set[str] = set()
        for entry in self._raw.values():
            for v in entry.get("labels", {}).values():
                if v:
                    phrases.add(v)
                    head = v.split()[0]
                    if head and head != v:
                        phrases.add(head)
            for v in entry.get("alts", []):
                if v:
                    phrases.add(v)
                    head = v.split()[0]
                    if head and head != v:
                        phrases.add(head)
        self._strip_phrases = sorted(phrases, key=len, reverse=True)

        # Substitution groups: only concepts whose primary labels are
        # all single-word qualify. Currently that's just Q1248784
        # (airport), which gives the clean group {Flughafen, aéroport,
        # aeroporto, airport}. Q55488 (railway station) and friends
        # have multi-word labels ("gare ferroviaire", "stazione
        # ferroviaria") that don't substitute sensibly inside canonical
        # station names. They still benefit the strip path above.
        self._groups: list[set[str]] = []
        for entry in self._raw.values():
            labels = [v for v in entry.get("labels", {}).values() if v]
            if not labels or any(" " in v for v in labels):
                continue
            group = set(labels)
            if len(group) >= 2:
                self._groups.append(group)

    def strip_phrases(self) -> list[str]:
        return self._strip_phrases

    def groups(self) -> list[set[str]]:
        return self._groups


def strip_wrappers(s: str, vocab: WrapperVocab) -> str:
    """Strip leading/trailing transit-vocabulary phrases (and the
    universal prepositions that may follow / precede them) from a
    Wikidata altLabel. Longest-match-first so multi-word phrases like
    "railway station" win over the substring "station"."""
    out = s.strip()
    phrases = vocab.strip_phrases()
    for _ in range(4):
        before = out
        out = _strip_prefix(out, phrases)
        out = _strip_suffix(out, phrases)
        out = out.strip()
        if out == before:
            break
    return out


def _strip_leading_preposition(s: str) -> str:
    """Strip one leading preposition/article (word-form or apostrophe-form)."""
    lower = s.lower()
    for prep in PREPOSITIONS_APOSTROPHE:
        if lower.startswith(prep):
            return s[len(prep):].lstrip()
    for prep in PREPOSITIONS_WORD:
        if lower.startswith(prep + " ") or lower == prep:
            return s[len(prep):].lstrip()
    return s


def _strip_prefix(s: str, phrases: list[str]) -> str:
    lower = s.lower()
    for phrase in phrases:
        p = phrase.lower()
        if lower.startswith(p + " ") or lower == p:
            return _strip_leading_preposition(s[len(phrase):].lstrip())
    return s


def _strip_suffix(s: str, phrases: list[str]) -> str:
    lower = s.lower()
    for phrase in phrases:
        p = phrase.lower()
        if lower.endswith(" " + p) or lower == p:
            cut = s[: -len(phrase)].rstrip()
            # Trailing prepositions/articles after stripping a transit
            # phrase (e.g. "X of the" → "X").
            for prep in PREPOSITIONS_WORD:
                if cut.lower().endswith(" " + prep) or cut.lower() == prep:
                    cut = cut[: -len(prep)].rstrip()
                    break
            return cut
    return s


def transit_substitutions(canonical: str, vocab: WrapperVocab) -> list[str]:
    """Swap a single transit-class word in the canonical with its
    other-language equivalents from the same Wikidata concept. So
    "Zürich Flughafen" generates "Zürich Aéroport", "Zürich Airport",
    "Zürich Aeroporto" — each member of the airport concept's group.

    Substitution is whole-word, case-insensitive. Only single-word forms
    in the group participate so canonicals stay sensible (no swap for
    multi-word forms like "stazione ferroviaria")."""
    aliases: set[str] = set()
    for group in vocab.groups():
        for word in group:
            if " " in word:
                continue
            pattern = re.compile(rf"\b{re.escape(word)}\b", re.IGNORECASE)
            if pattern.search(canonical):
                for other in group:
                    if " " in other or other.lower() == word.lower():
                        continue
                    alias = pattern.sub(other, canonical)
                    if alias and alias != canonical:
                        aliases.add(alias)
    return sorted(aliases)


def city_substitution_aliases(canonical: str, city_labels: dict) -> list[str]:
    """For a station whose canonical starts with one of its city's
    multilingual labels (possibly followed by a separator), substitute
    each *other* label and emit the resulting name as an alias.

    Examples:
        canonical="Genève",          labels={de:"Genf",fr:"Genève",...}
            → ["Genf", "Geneva", "Ginevra", "Genevra"]
        canonical="Genève-Aéroport", labels={de:"Genf",fr:"Genève",...}
            → ["Genf-Aéroport", "Geneva-Aéroport", ...]
        canonical="Brig",            labels={de:"Brig-Glis",fr:"Brigue-Glis",...}
            → ["Brigue", "Briga"] (shared-suffix case)
    """
    labels = sorted({l for l in city_labels.values() if l and isinstance(l, str)})
    if not labels:
        return []

    aliases: set[str] = set()

    # Direction 1: a city label is a prefix of canonical.
    best_prefix = None
    for label in labels:
        if canonical == label or (
            len(canonical) > len(label)
            and canonical.startswith(label)
            and canonical[len(label)] in " ,-"
        ):
            if best_prefix is None or len(label) > len(best_prefix):
                best_prefix = label

    if best_prefix is not None:
        suffix = canonical[len(best_prefix):]
        for other in labels:
            if other == best_prefix:
                continue
            alias = other + suffix
            if alias and alias != canonical:
                aliases.add(alias)
    else:
        # Direction 2: shared trailing suffix across labels; canonical
        # equals one of the bare forms.
        common = longest_common_suffix(labels)
        if common:
            bare = [l[: -len(common)] for l in labels]
            if canonical in bare:
                for b in bare:
                    if b and b != canonical:
                        aliases.add(b)

    aliases.discard(canonical)
    return sorted(aliases)


def longest_common_suffix(strings: list[str]) -> str:
    if len(strings) < 2:
        return ""
    common = strings[0]
    for s in strings[1:]:
        i = 0
        while i < len(common) and i < len(s) and common[-i - 1] == s[-i - 1]:
            i += 1
        common = common[-i:] if i > 0 else ""
        if not common:
            return ""
    return common


# Length cap on plausible altLabels — twice the longest SBB canonical
# station name (30 chars × 2 = 60). Any Wikidata altLabel longer than
# this is almost certainly a description that someone pasted into
# skos:altLabel by mistake. Threshold is data-derived from the
# canonical-name distribution, not a magic constant.
ALT_LENGTH_CAP = 60


def is_description_not_name(alt: str) -> bool:
    """Two-part heuristic for Wikidata altLabels that are actually
    building descriptions miscategorised under skos:altLabel.

    Wikidata distinguishes:
      rdfs:label          primary name
      skos:altLabel       alternative name
      schema:description  description (not a name)

    Some editors paste description text into skos:altLabel by mistake.
    Real examples from Bex / Alpnachstad / Bäretswil / Neuthal:
        "CFF station: passenger building, buffet, depot, shelter, ..."
        "Bahnhof Alpnachstad (1889) mit Lokremise (dislozierter Teil ...)"
        "Bahnhof Bäretswil DVZO (ehemals UeBB), Aufnahmegebäude mit ..."
        "Bahnhof Neuthal UeBB, Aufnahmegebäude/Güterschuppen (heute Wohnhaus)"

    Two structural signals, applied as OR:

    1. **Colon** — no real Swiss station name uses ':'. The pattern
       "<title>: <description>" is unmistakeable description syntax.
       Audited 2026-05-24 against the full Wikidata snapshot (2 231
       TRAIN altLabels): drops exactly 3 Bex rows, zero false positives.

    2. **Length cap of 60 chars** = 2× the longest canonical SBB
       station name (data-derived, not picked). Catches the
       description rows that don't use colon — Alpnachstad (95),
       Bäretswil (107), Neuthal (68). Five descriptions in the 30-55
       char range slip through (Embrach-Rorbas, Dornach-Arlesheim,
       Sihlwald, Bahnhofgebäude-style, Wartehalle-style) — they share
       a length distribution with real altLabels like "gare de
       Biel/Bienne Bözingenfeld/Champs-de-Boujean" (50), so no
       higher-precision length cap is possible. Better to keep five
       harmless description-y aliases than drop a real one.

    A more principled fix would query Wikidata for the building-part
    vocabulary (Aufnahmegebäude, Güterschuppen, Lokremise, …) and use
    presence of any such word as the signal. That requires
    enumerating the right Q-IDs by hand, which is itself curation;
    deferred."""
    return ":" in alt or len(alt) > ALT_LENGTH_CAP


def derive_aliases(canonical: str, wd_entry: dict, vocab: WrapperVocab) -> list[str]:
    """All alias sources combined. wd_entry holds city labels (under their
    language keys), 'alts' (list of skos:altLabel values from the station
    entity), and 'iata' (IATA code from associated airport).
    vocab holds the universal transit vocabulary loaded from Wikidata."""
    aliases: set[str] = set()

    # 1. City multilingual labels via prefix substitution. Filtering of
    #    admin-region entities (canton / district / region) is enforced
    #    at SPARQL fetch time via the Wikidata ontology — see
    #    scripts/fetch_wikidata_aliases.py.
    city_labels = {
        k: v
        for k, v in wd_entry.items()
        if k in ("de", "fr", "it", "en", "rm") and v
    }
    aliases.update(city_substitution_aliases(canonical, city_labels))

    # 2. Station altLabels, wrapper-stripped using Wikidata vocab.
    #    Skip Wikidata description text miscategorised as altLabel
    #    (detected by colon — see is_description_not_name docstring).
    for raw in wd_entry.get("alts", []):
        if is_description_not_name(raw):
            continue
        stripped = strip_wrappers(raw, vocab)
        if stripped and stripped != canonical and len(stripped) >= 2:
            aliases.add(stripped)

    # 3. IATA airport code, if present
    iata = wd_entry.get("iata", "")
    if iata and len(iata) == 3:
        aliases.add(iata)

    # 4. Transit-class word substitutions driven by Wikidata concept
    #    groups (railway station / central station / airport / metro
    #    station / tram stop).
    aliases.update(transit_substitutions(canonical, vocab))

    aliases.discard(canonical)
    return sorted(aliases)


def main(stations_path: str, aliases_path: str, vocab_path: str, out_path: str) -> None:
    with open(stations_path, encoding="utf-8") as f:
        stations = json.load(f)
    with open(aliases_path, encoding="utf-8") as f:
        wikidata = json.load(f)
    vocab = WrapperVocab(vocab_path)

    enriched_count = 0
    total_aliases = 0
    for s in stations:
        # Always drop stale aliases first — re-runs must overwrite, not merge.
        s.pop("aliases", None)
        # Aliases only emitted for train stations — the rest would flood
        # the popover with near-duplicates of the city result and most are
        # not worth cross-language anyway.
        if s.get("mode") != "TRAIN":
            continue
        uic = s.get("uic")
        if not uic:
            continue
        wd_entry = wikidata.get(uic, {})
        aliases = derive_aliases(s["name"], wd_entry, vocab)
        if aliases:
            s["aliases"] = aliases
            enriched_count += 1
            total_aliases += len(aliases)

    with open(out_path, "w", encoding="utf-8") as f:
        json.dump(stations, f, ensure_ascii=False, separators=(",", ":"))

    print(
        f"enriched {enriched_count} of {len(stations)} stations "
        f"({total_aliases} total aliases) → {out_path}"
    )


if __name__ == "__main__":
    if len(sys.argv) != 5:
        print(
            "usage: merge_wikidata_aliases.py <stations.json> "
            "<wikidata-aliases.json> <wikidata-vocab.json> <out.json>",
            file=sys.stderr,
        )
        sys.exit(2)
    main(sys.argv[1], sys.argv[2], sys.argv[3], sys.argv[4])
