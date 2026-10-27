-- +goose Up
INSERT INTO labels (type, slug, name, name_he, icon, sort_order) VALUES
('category','pizza',        'Pizza',             'פיצה',             '🍕', 10),
('category','bread_dough',  'Bread & Dough',     'לחם ובצקים',       '🍞', 20),
('category','beef',         'Beef',              'בקר',              '🥩', 30),
('category','lamb',         'Lamb',              'טלה',              '🍖', 40),
('category','pork',         'Pork',              'חזיר',             '🥓', 50),
('category','poultry',      'Chicken & Poultry', 'עוף',              '🍗', 60),
('category','fish_seafood', 'Fish & Seafood',    'דגים ופירות ים',   '🐟', 70),
('category','vegetables',   'Vegetables',        'ירקות',            '🥦', 80),
('category','sides',        'Sides',             'תוספות',           '🥔', 90),
('category','desserts',     'Desserts',          'קינוחים',          '🍰', 100),
('category','snacks_dried', 'Snacks & Dried',    'חטיפים ומיובשים',  '🥨', 110),
('category','other',        'Other',             'אחר',              '🍽️', 120);

-- +goose Down
DELETE FROM labels WHERE type = 'category' AND created_by IS NULL;
