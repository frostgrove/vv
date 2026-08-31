package crud

type PaginatedResponse[T any] struct {
	Items      []T   `json:"items"`
	Page       int   `json:"page"`
	Limit      int   `json:"limit"`
	Total      int64 `json:"total"`
	TotalPages int   `json:"totalPages"`
	HasNext    bool  `json:"hasNext"`
	HasPrev    bool  `json:"hasPrev"`

	NextCursor string `json:"nextCursor,omitempty"`
	PrevCursor string `json:"prevCursor,omitempty"`
}

func NewPaginatedResponse[T any](items []T, page, limit int, total int64) PaginatedResponse[T] {
	if items == nil {
		items = []T{}
	}
	r := PaginatedResponse[T]{
		Items: items,
		Page:  page,
		Limit: limit,
		Total: total,
	}
	if limit > 0 {
		r.TotalPages = int((total + int64(limit) - 1) / int64(limit))
	} else if len(items) > 0 {
		r.TotalPages = 1
	}
	r.HasPrev = page > 1
	r.HasNext = r.TotalPages > 0 && page < r.TotalPages
	return r
}

func (this PaginatedResponse[T]) IsEmpty() bool { return len(this.Items) == 0 }

func MapPage[A, B any](p PaginatedResponse[A], f func(A) B) PaginatedResponse[B] {
	out := make([]B, len(p.Items))
	for i, v := range p.Items {
		out[i] = f(v)
	}
	return PaginatedResponse[B]{
		Items:      out,
		Page:       p.Page,
		Limit:      p.Limit,
		Total:      p.Total,
		TotalPages: p.TotalPages,
		HasNext:    p.HasNext,
		HasPrev:    p.HasPrev,
		NextCursor: p.NextCursor,
		PrevCursor: p.PrevCursor,
	}
}
