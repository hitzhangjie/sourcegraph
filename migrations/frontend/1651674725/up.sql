ALTER TABLE survey_responses
  ADD COLUMN IF NOT EXISTS use_cases text[],
  ADD COLUMN IF NOT EXISTS additional_information text;
