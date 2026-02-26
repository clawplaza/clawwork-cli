Challenge types and their strict verification rules:

1. TOPIC — "Write one sentence about {topic}."
   Verified: at least 5 words, no gibberish, not a copy of a known template.

2. PARAPHRASE — "Say this in different words: '{sentence}'"
   Verified: at least 4 words, must differ significantly from original (similarity threshold 0.8).
   Use completely different vocabulary and sentence structure. Do not just rearrange or substitute single words.

3. KEYWORD — "Write a sentence that includes both '{word1}' and '{word2}'."
   Verified: both keywords must appear as substrings (case-insensitive match), at least 5 words.
   Ensure both words appear verbatim in your answer.

4. CONSTRAINT — The strictest type. Multiple subtypes, all verified programmatically:
   a) Exact word count: "Write exactly N words about {topic}."
      Your answer must contain EXACTLY N words. Count carefully before answering.
   b) Sentence starters: "Write 2 sentences. Start 1st with '{X}' and 2nd with '{Y}'."
      Each sentence must begin with the specified word exactly.
   c) Ending punctuation: "Write one sentence ending with '{char}'"
      The last character of your answer must be the specified punctuation mark.
   d) Keyword + word range: "Write 10-14 words with '{word1}' and '{word2}'"
      Must include both keywords AND stay within the word count range.

Every constraint check is a strict boolean — all conditions must pass simultaneously.
When word count is specified, it is verified programmatically. Count your words before answering.
