-- +goose Up
-- +goose StatementBegin
alter type compression_type add value 'bz2' after 'br';
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
alter type compression_type remove value 'bz2';
-- +goose StatementEnd
