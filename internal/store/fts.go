package store

import (
	"context"
	"cooking/internal/domain"
	"errors"
	"strings"

	"cooking/internal/textnorm"
)

// ReindexRecipe rebuilds the FTS row of an active recipe (and removes it for inbox items / missing recipes).
func (q Q) ReindexRecipe(ctx context.Context, id string) error {
	if _, err := q.q.ExecContext(ctx, `DELETE FROM recipe_fts WHERE recipe_id=?`, id); err != nil {
		return err
	}
	r, err := q.GetRecipe(ctx, id)
	if err != nil {
		if errors.Is(err, domain.ErrNotFound) {
			return nil
		}
		return err
	}
	if r.Status != "active" {
		return nil
	}
	var ing, steps, labels []string
	for _, i := range r.Ingredients {
		ing = append(ing, i.RawText)
	}
	for _, s := range r.Steps {
		steps = append(steps, s.Text)
	}
	for _, l := range r.Labels {
		labels = append(labels, l.Name)
		if l.NameHe != "" {
			labels = append(labels, l.NameHe)
		}
	}
	notes := []string{r.Notes}
	logs, err := q.ListCookLogs(ctx, id)
	if err != nil {
		return err
	}
	for _, l := range logs {
		if l.Note != "" {
			notes = append(notes, l.Note)
		}
	}
	n := textnorm.Normalize
	_, err = q.q.ExecContext(ctx, `INSERT INTO recipe_fts (recipe_id, title, description, ingredients, steps, labels, notes) VALUES (?,?,?,?,?,?,?)`,
		id, n(r.Title), n(r.Description), n(strings.Join(ing, "\n")), n(strings.Join(steps, "\n")), n(strings.Join(labels, " ")), n(strings.Join(notes, "\n")))
	return err
}
